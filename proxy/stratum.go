package proxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"time"

	"github.com/sammy007/open-ethereum-pool/util"
)

const (
	MaxReqSize = 1024
)

func (s *ProxyServer) ListenTCP(stratum_id int) {
	timeout := util.MustParseDuration(s.config.Proxy.Stratums[stratum_id].Timeout)
	s.stratums[stratum_id].timeout = timeout

	addr, err := net.ResolveTCPAddr("tcp", s.config.Proxy.Stratums[stratum_id].Listen)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	server, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	defer server.Close()

	log.Printf("Stratum[%d] of Difficulty[%d] listening on %s", stratum_id, s.config.Proxy.Stratums[stratum_id].Difficulty, s.config.Proxy.Stratums[stratum_id].Listen)
	var accept = make(chan int, s.config.Proxy.Stratums[stratum_id].MaxConn)
	n := 0

	for {
		conn, err := server.AcceptTCP()
		if err != nil {
			continue
		}
		conn.SetKeepAlive(true)

		ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())

		if s.policy.IsBanned(ip) || !s.policy.ApplyLimitPolicy(ip) {
			conn.Close()
			continue
		}
		n += 1
		cs := &Session{stratum_id: stratum_id, conn: conn, ip: ip}

		accept <- n
		go func(cs *Session) {
			err = s.handleTCPClient(cs)
			if err != nil {
				s.removeSession(cs)
				conn.Close()
			}
			<-accept
		}(cs)
	}
}

func (s *ProxyServer) handleTCPClient(cs *Session) error {
	cs.enc = json.NewEncoder(cs.conn)
	connbuff := bufio.NewReaderSize(cs.conn, MaxReqSize)
	s.setDeadline(cs.conn, cs.stratum_id)

	for {
		data, isPrefix, err := connbuff.ReadLine()
		if isPrefix {
			log.Printf("Socket flood detected from %s", cs.ip)
			s.policy.BanClient(cs.ip)
			return err
		} else if err == io.EOF {
			log.Printf("Client %s disconnected", cs.ip)
			s.removeSession(cs)
			break
		} else if err != nil {
			log.Printf("Error reading from socket: %v", err)
			return err
		}

		if len(data) > 1 {
			var req StratumReq
			err = json.Unmarshal(data, &req)
			if err != nil {
				s.policy.ApplyMalformedPolicy(cs.ip)
				log.Printf("Malformed stratum request from %s: %v", cs.ip, err)
				return err
			}
			s.setDeadline(cs.conn, cs.stratum_id)
			err = cs.handleTCPMessage(s, &req)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (cs *Session) handleTCPMessage(s *ProxyServer, req *StratumReq) error {
	// Handle RPC methods
	switch req.Method {
	case "eth_submitLogin":
		var params []string
		err := json.Unmarshal(*req.Params, &params)
		if err != nil {
			log.Println("Malformed stratum request params from", cs.ip)
			return err
		}
		reply, errReply := s.handleLoginRPC(cs, params, req.Worker)
		if errReply != nil {
			return cs.sendTCPError(req.Id, errReply)
		}
		return cs.sendTCPResult(req.Id, reply)
	case "eth_getWork":
		reply, errReply := s.handleGetWorkRPC(cs)
		if errReply != nil {
			return cs.sendTCPError(req.Id, errReply)
		}
		return cs.sendTCPResult(req.Id, &reply)
	case "eth_submitWork":
		var params []string
		err := json.Unmarshal(*req.Params, &params)
		if err != nil {
			log.Println("Malformed stratum request params from", cs.ip)
			return err
		}
		reply, errReply := s.handleTCPSubmitRPC(cs, req.Worker, params)
		if errReply != nil {
			return cs.sendTCPError(req.Id, errReply)
		}
		return cs.sendTCPResult(req.Id, &reply)
	case "eth_submitHashrate":
		return cs.sendTCPResult(req.Id, true)
	default:
		errReply := s.handleUnknownRPC(cs, req.Method)
		return cs.sendTCPError(req.Id, errReply)
	}
}

func (cs *Session) sendTCPResult(id *json.RawMessage, result interface{}) error {
	cs.Lock()
	defer cs.Unlock()

	message := JSONRpcResp{Id: id, Version: "2.0", Error: nil, Result: result}
	return cs.enc.Encode(&message)
}

func (cs *Session) pushNewJob(result interface{}) error {
	cs.Lock()
	defer cs.Unlock()
	// FIXME: Temporarily add ID for Claymore compliance
	message := JSONPushMessage{Version: "2.0", Result: result, Id: 0}
	return cs.enc.Encode(&message)
}

func (cs *Session) sendTCPError(id *json.RawMessage, reply *ErrorReply) error {
	cs.Lock()
	defer cs.Unlock()

	message := JSONRpcResp{Id: id, Version: "2.0", Error: reply}
	err := cs.enc.Encode(&message)
	if err != nil {
		return err
	}
	return errors.New(reply.Message)
}

func (self *ProxyServer) setDeadline(conn *net.TCPConn, stratum_id int) {
	conn.SetDeadline(time.Now().Add(self.stratums[stratum_id].timeout))
}

func (s *ProxyServer) registerSession(cs *Session) {
	s.stratums[cs.stratum_id].sessionsMu.Lock()
	defer s.stratums[cs.stratum_id].sessionsMu.Unlock()
	s.stratums[cs.stratum_id].sessions[cs] = struct{}{}
}

func (s *ProxyServer) removeSession(cs *Session) {
	s.stratums[cs.stratum_id].sessionsMu.Lock()
	defer s.stratums[cs.stratum_id].sessionsMu.Unlock()
	delete(s.stratums[cs.stratum_id].sessions, cs)
}

func (s *ProxyServer) broadcastNewJobs(stratum_id int) {
	t := s.currentBlockTemplate()
	if t == nil || len(t.Header) == 0 || s.isSick() {
		return
	}

	stratum := s.stratums[stratum_id]

	reply := []string{t.Header, t.Seed, stratum.diff}

	stratum.sessionsMu.RLock()
	defer stratum.sessionsMu.RUnlock()

	count := len(stratum.sessions)
	log.Printf("Broadcasting new job to %v stratum miners", count)

	start := time.Now()
	bcast := make(chan int, 1024)
	n := 0

	for m, _ := range stratum.sessions {
		n++
		bcast <- n

		go func(cs *Session) {
			err := cs.pushNewJob(&reply)
			<-bcast
			if err != nil {
				log.Printf("Job transmit error to %v@%v: %v", cs.login, cs.ip, err)
				s.removeSession(cs)
			} else {
				s.setDeadline(cs.conn, cs.stratum_id)
			}
		}(m)
	}
	log.Printf("Jobs broadcast finished %s", time.Since(start))
}
