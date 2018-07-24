package rpc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	//"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"github.com/sammy007/open-ethereum-pool/util"
)

type RPCClient struct {
	sync.RWMutex
	Url         string
	Name        string
	Account     string
	Password    string
	sick        bool
	sickRate    int
	successRate int
	client      *http.Client
}

type GetBalanceReply struct {
	Unspent int64 `json:"unspent"`
	Frozen int64 `json:"frozen"`
}


type GetPeerCountReply struct {
	Peers []string `json:"peers"`
}

type MVSTxOutput struct {
	Address     string `json:"address"`
	Value       int64 `json:"value"`
}

type MVSTx struct {
	Hash     string `json:"hash"`
	Locktime string `json:"lock_time"`
	Version  string `json:"version"`
	Outputs  []MVSTxOutput `json:"outputs"`
}

type MVSSignRawTxReply struct {
	Hash     string `json:"hash"`
	RawTx string `json:"rawtx"`
}

/*
type GetBlockReply struct {
	Number       string   `json:"number"`
	Hash         string   `json:"hash"`
	Nonce        string   `json:"nonce"`
	Miner        string   `json:"miner"`
	Difficulty   string   `json:"difficulty"`
	GasLimit     string   `json:"gasLimit"`
	GasUsed      string   `json:"gasUsed"`
	Transactions []Tx     `json:"transactions"`
	Uncles       []string `json:"uncles"`
	// https://github.com/ethereum/EIPs/issues/95
	SealFields []string `json:"sealFields"`
}*/

type GetBlockReply struct {
	Difficulty       string `json:"bits"`
	Hash             string `json:"hash"`
	MerkleTreeHash   string `json:"merkle_tree_hash"`
	Nonce            string `json:"nonce"`
	PrevHash         string `json:"previous_block_hash"`
	TimeStamp        uint32 `json:"time_stamp"`
	Version          int32 `json:"version"`
	Mixhash          string `json:"mixhash"`
	Number           int64 `json:"number"`
	TransactionCount int32 `json:"transaction_count"`
	Transactions     []MVSTx `json:"transactions"`
}

type GetBlockReplyPart struct {
	Number     uint64 `json:"number"`
	Difficulty string `json:"bits"`
}

const receiptStatusSuccessful = "0x1"

type TxReceipt struct {
	TxHash string `json:"hash"`
	Height int64  `json:"height"`
	//TxHash    string `json:"transactionHash"`
	//GasUsed   string `json:"gasUsed"`
	//BlockHash string `json:"blockHash"`
	//Status    string `json:"status"`
}

func (r *TxReceipt) Confirmed() bool {
	//return len(r.BlockHash) > 0
	return r.Height != 0
}

// Use with previous method
func (r *TxReceipt) Successful() bool {
	/*if len(r.Status) > 0 {
		return r.Status == receiptStatusSuccessful
	}*/
	return true
}

type JSONRpcResp struct {
	Id     *json.RawMessage       `json:"id"`
	Result *json.RawMessage       `json:"result"`
	Error  map[string]interface{} `json:"error"`
}

func NewRPCClient(name, url, account, password, timeout string) *RPCClient {
	rpcClient := &RPCClient{Name: name, Url: url, Account: account, Password: password}
	timeoutIntv := util.MustParseDuration(timeout)
	rpcClient.client = &http.Client{
		Timeout: timeoutIntv,
	}
	return rpcClient
}

func (r *RPCClient) GetWork() ([]string, error) {
	//rpcResp, err := r.doPost(r.Url, "eth_getWork", []string{})
	rpcResp, err := r.doPost(r.Url, "getwork", []string{})
	if err != nil {
		return nil, err
	}
	var reply []string
	err = json.Unmarshal(*rpcResp.Result, &reply)
	return reply, err
}

func (r *RPCClient) SetAddress(address string) (string, error) {
	rpcResp, err := r.doPost(r.Url, "setminingaccount", []string{r.Account, r.Password, address})
	if err != nil {
		return "post fail", err
	}
	var reply string
	err = json.Unmarshal(*rpcResp.Result, &reply)
	return reply, err
}

func (r *RPCClient) GetHeight() (int64, error) {
	rpcResp, err := r.doPost(r.Url, "fetch-height", []string{})
	if err != nil {
		return 0, err
	}
	var height int64
	err = json.Unmarshal(*rpcResp.Result, &height)
	return height, err
}

func (r *RPCClient) GetPendingBlock() (*GetBlockReplyPart, error) {
	rpcResp, err := r.doPost(r.Url, "fetchheaderext", []interface{}{r.Account, r.Password, "pending"})
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		var reply *GetBlockReplyPart
		err = json.Unmarshal(*rpcResp.Result, &reply)
		return reply, err
	}
	return nil, nil
}

func (r *RPCClient) GetBlockByHeight(height int64) (*GetBlockReply, error) {
	params := []interface{}{height}
	return r.getBlockBy("getblock", params)
}

func (r *RPCClient) GetBlockByHash(hash string) (*GetBlockReply, error) {
	params := []interface{}{hash}
	return r.getBlockBy("getblock", params)
}

func (r *RPCClient) GetUncleByBlockNumberAndIndex(height int64, index int) (*GetBlockReply, error) {
	params := []interface{}{fmt.Sprintf("0x%x", height), fmt.Sprintf("0x%x", index)}
	return r.getBlockBy("eth_getUncleByBlockNumberAndIndex", params)
}

func (r *RPCClient) getBlockBy(method string, params []interface{}) (*GetBlockReply, error) {
	rpcResp, err := r.doPost(r.Url, method, params)
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		var reply *GetBlockReply
		err = json.Unmarshal(*rpcResp.Result, &reply)
		return reply, err
	}
	return nil, nil
}

func (r *RPCClient) GetTxReceipt(hash string) (*TxReceipt, error) {
	rpcResp, err := r.doPost(r.Url, "gettx", []string{hash})
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		var reply *TxReceipt
		err = json.Unmarshal(*rpcResp.Result, &reply)
		return reply, err
	}
	return nil, nil
}

func (r *RPCClient) SubmitBlock(params []string) (bool, error) {
	//rpcResp, err := r.doPost(r.Url, "eth_submitWork", params)
	rpcResp, err := r.doPost(r.Url, "submitwork", params)
	if err != nil {
		return false, err
	}
	var reply bool
	//err = json.Unmarshal(*rpcResp.Result, &reply_str)
	fmt.Println(*rpcResp.Result)
	if string(*rpcResp.Result) == "\"false\"" {
		reply = false
	} else {
		reply = true
	}
	return reply, err
}

func (r *RPCClient) GetBalance(address string) (*big.Int, error) {
	rpcResp, err := r.doPost(r.Url, "getaddressetp", []string{address})
	if err != nil {
		return nil, err
	}
	var reply GetBalanceReply
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return nil, err
	}
	return big.NewInt(reply.Unspent - reply.Frozen), err
}

func (r *RPCClient) Sign(from string, s string) (string, error) {
	hash := sha256.Sum256([]byte(s))
	rpcResp, err := r.doPost(r.Url, "eth_sign", []string{from, common.ToHex(hash[:])})
	var reply string
	if err != nil {
		return reply, err
	}
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return reply, err
	}
	if util.IsZeroHash(reply) {
		err = errors.New("Can't sign message, perhaps account is locked")
	}
	return reply, err
}

func (r *RPCClient) GetPeerCount() (int64, error) {
	rpcResp, err := r.doPost(r.Url, "getpeerinfo", nil)
	if err != nil {
		return 0, err
	}
	var reply GetPeerCountReply
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return 0, err
	}
	return int64(len(reply.Peers)), nil
}

func (r *RPCClient) SendTransaction(from, to, value string) (string, error) {
	rpcResp, err := r.doPost(r.Url, "sendfrom", []string{r.Account, r.Password, from, to, value})
	if err != nil {
		return "", err
	}

	var reply MVSTx
	err = json.Unmarshal(*rpcResp.Result, &reply)
	fmt.Println("json.Unmarshal", err)
	if err != nil {
		return "", err
	}

	return reply.Hash, err
}

/*
   :param: type(uint16_t): "Transaction type. 0 -- transfer etp, 1 -- deposit etp, 3 -- transfer asset"
   :param: senders(list of string): "Send from addresses"
   :param: receivers(list of string): "Send to [address:amount]. amount is asset number if sybol option specified"
   :param: symbol(std::string): "asset name, not specify this option for etp tx"
   :param: deposit(uint16_t): "Deposits support [7, 30, 90, 182, 365] days. defaluts to 7 days"
   :param: mychange(std::string): "Mychange to this address, includes etp and asset change"
   :param: message(std::string): "Message/Information attached to this transaction"
   :param: fee(uint64_t): "Transaction fee. defaults to 10000 ETP bits"
*/
func (r *RPCClient) createRawTX(type_ uint16, senders []string, receivers []string, symbol string, deposit uint16, mychange string, message string, fee uint64) (string, error) {
	cmd := "createrawtx"
	positional := []interface{}{}

	optional := map[string]interface{}{
		"type":      type_,
		"senders":   senders,
		"receivers": receivers,
	}

	if symbol != "" {
		optional["symbol"] = symbol
	}
	if deposit != 0 {
		optional["deposit"] = deposit
	}
	if mychange != "" {
		optional["mychange"] = mychange
	}
	if message != "" {
		optional["message"] = message
	}
	if fee != 0 {
		optional["fee"] = fee
	}
	args := append(positional, optional)
	rpcResp, err := r.doPost(r.Url, cmd, args)
	if err != nil {
		return "", err
	}
	var rawtx string
	err = json.Unmarshal(*rpcResp.Result, &rawtx)
	if err != nil {
		return "", err
	}

	return rawtx, err
}
/*
   :param: ACCOUNTNAME(std::string): Account name required.
   :param: ACCOUNTAUTH(std::string): Account password(authorization) required.
   :param: TRANSACTION(string of hexcode): "The input Base16 transaction to sign."
*/
func (r *RPCClient) signRawTX(TRANSACTION string) (string, error) {
	cmd := "signrawtx"
	positional := []interface{}{r.Account, r.Password, TRANSACTION}

	optional := map[string]interface{}{}

	args := append(positional, optional)
	rpcResp, err := r.doPost(r.Url, cmd, args)
	if err != nil {
		return "", err
	}

	var rawtx MVSSignRawTxReply
	err = json.Unmarshal(*rpcResp.Result, &rawtx)
	if err != nil {
		return "", err
	}

	return rawtx.RawTx, err
}

/*
   :param: TRANSACTION(string of hexcode): "The input Base16 transaction to broadcast."
   :param: fee(uint64_t): "The max tx fee. default_value 10 etp"
*/
func (r *RPCClient) sendRawTX(TRANSACTION string, fee uint64) (string, error) {
	cmd := "sendrawtx"
	positional := []interface{}{TRANSACTION}

	optional := map[string]interface{}{}

	if fee != 0 {
		optional["fee"] = fee
	}
	args := append(positional, optional)
	rpcResp, err := r.doPost(r.Url, cmd, args)
	if err != nil {
		return "", err
	}

	var txhash string
	err = json.Unmarshal(*rpcResp.Result, &txhash)
	if err != nil {
		return "", err
	}

	return txhash, err
}

func (r *RPCClient) SendMore(from string, receivers map[string]int64 ) (string, error) {
	var	receivers_ []string
	var senders []string
	for	login, amount := range receivers {
		receivers_ = append(receivers_, login + ":" + strconv.FormatInt(amount, 10))
	}
	senders = append(senders, from)
	rawtx1, err1 := r.createRawTX(0, senders, receivers_, "", 0, from, "", 10000)
	if err1 != nil {
		return "createRawTX Failed", err1
	}

	rawtx2, err2 := r.signRawTX(rawtx1)
	if err2 != nil {
		return "signRawTX Failed", err2
	}

	return r.sendRawTX(rawtx2, 10000)
}


func (r *RPCClient) doPost(url string, method string, params interface{}) (*JSONRpcResp, error) {
	jsonReq := map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params, "id": 0}
	data, _ := json.Marshal(jsonReq)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Length", (string)(len(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp *JSONRpcResp
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}
	if rpcResp.Error != nil {
		r.markSick()
		return nil, errors.New(rpcResp.Error["message"].(string))
	}
	return rpcResp, err
}

func (r *RPCClient) Check() bool {
	_, err := r.GetWork()
	if err != nil {
		return false
	}
	r.markAlive()
	return !r.Sick()
}

func (r *RPCClient) Sick() bool {
	r.RLock()
	defer r.RUnlock()
	return r.sick
}

func (r *RPCClient) markSick() {
	r.Lock()
	r.sickRate++
	r.successRate = 0
	if r.sickRate >= 5 {
		r.sick = true
	}
	r.Unlock()
}

func (r *RPCClient) markAlive() {
	r.Lock()
	r.successRate++
	if r.successRate >= 5 {
		r.sick = false
		r.sickRate = 0
		r.successRate = 0
	}
	r.Unlock()
}
