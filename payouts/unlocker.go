package payouts

import (
	"fmt"
	"log"
	_math "math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common/math"

	"github.com/sammy007/open-ethereum-pool/rpc"
	"github.com/sammy007/open-ethereum-pool/storage"
	"github.com/sammy007/open-ethereum-pool/util"
)

type UnlockerConfig struct {
	Enabled        bool    `json:"enabled"`
	PoolFee        float64 `json:"poolFee"`
	PoolFeeAddress string  `json:"poolFeeAddress"`
	Donate         bool    `json:"donate"`
	Depth          int64   `json:"depth"`
	ImmatureDepth  int64   `json:"immatureDepth"`
	KeepTxFees     bool    `json:"keepTxFees"`
	Interval       string  `json:"interval"`
	Daemon         string  `json:"daemon"`
	Timeout        string  `json:"timeout"`
	Account        string
	Password       string
	Address        string
}

const minDepth = 16
const byzantiumHardForkHeight = 4370000

var homesteadReward = math.MustParseBig256("5000000000000000000")
var byzantiumReward = math.MustParseBig256("3000000000000000000")

// Donate 10% from pool fees to developers
const donationFee = 10.0
const donationAccount = "0xb85150eb365e7df0941f0cf08235f987ba91506a"

type BlockUnlocker struct {
	config   *UnlockerConfig
	backend  *storage.RedisClient
	rpc      *rpc.RPCClient
	halt     bool
	lastFail error
}

func NewBlockUnlocker(cfg *UnlockerConfig, backend *storage.RedisClient) *BlockUnlocker {
	if len(cfg.PoolFeeAddress) != 0 && !util.IsValidBitcoinAddress(cfg.PoolFeeAddress) {
		log.Fatalln("Invalid poolFeeAddress", cfg.PoolFeeAddress)
	}
	if cfg.Depth < minDepth*2 {
		log.Fatalf("Block maturity depth can't be < %v, your depth is %v", minDepth*2, cfg.Depth)
	}
	if cfg.ImmatureDepth < minDepth {
		log.Fatalf("Immature depth can't be < %v, your depth is %v", minDepth, cfg.ImmatureDepth)
	}
	u := &BlockUnlocker{config: cfg, backend: backend}
	u.rpc = rpc.NewRPCClient("BlockUnlocker", cfg.Daemon, cfg.Account, cfg.Password, cfg.Timeout)
	return u
}

func (u *BlockUnlocker) Start() {
	log.Println("Starting block unlocker")
	intv := util.MustParseDuration(u.config.Interval)
	timer := time.NewTimer(intv)
	log.Printf("Set block unlock interval to %v", intv)

	// Immediately unlock after start
	u.unlockPendingBlocks()
	u.unlockAndCreditMiners()
	timer.Reset(intv)

	go func() {
		for {
			select {
			case <-timer.C:
				u.unlockPendingBlocks()
				u.unlockAndCreditMiners()
				timer.Reset(intv)
			}
		}
	}()
}

type UnlockResult struct {
	maturedBlocks  []*storage.BlockData
	orphanedBlocks []*storage.BlockData
	orphans        int
	uncles         int
	blocks         int
}

/* Geth does not provide consistent state when you need both new height and new job,
 * so in redis I am logging just what I have in a pool state on the moment when block found.
 * Having very likely incorrect height in database results in a weird block unlocking scheme,
 * when I have to check what the hell we actually found and traversing all the blocks with height-N and height+N
 * to make sure we will find it. We can't rely on round height here, it's just a reference point.
 * ISSUE: https://github.com/ethereum/go-ethereum/issues/2333
 */
func (u *BlockUnlocker) unlockCandidates(candidates []*storage.BlockData) (*UnlockResult, error) {
	result := &UnlockResult{}

	// Data row is: "height:nonce:powHash:mixDigest:timestamp:diff:totalShares"
	for _, candidate := range candidates {
		height := candidate.Height

		block, err := u.rpc.GetBlockByHeight(height)
		if err != nil {
			log.Printf("Error while retrieving block %v from node: %v", height, err)
			return nil, err
		}
		if block == nil {
			return nil, fmt.Errorf("Error while retrieving block %v from node, wrong node height", height)
		}

		if u.matchCandidate(block, candidate) {
			result.blocks++

			err = u.handleBlock(block, candidate)
			if err != nil {
				u.halt = true
				u.lastFail = err
				return nil, err
			}
			result.maturedBlocks = append(result.maturedBlocks, candidate)
			log.Printf("Mature block %v, hash: %v", candidate.Height, candidate.Hash[0:10])
		} else {
			result.orphans++
			candidate.Orphan = true
			result.orphanedBlocks = append(result.orphanedBlocks, candidate)
			log.Printf("Orphaned block %v:%v", candidate.RoundHeight, candidate.Nonce)
		}
	}
	return result, nil
}

func (u *BlockUnlocker) matchCandidate(block *rpc.GetBlockReply, candidate *storage.BlockData) bool {
	log.Printf("========> calling matchCandidate %v:%v with coinbase address[%s]", candidate.RoundHeight, candidate.Nonce, block.Transactions[0].Outputs[0].Address)
	// check coinbase target address
	if block.Transactions[0].Outputs[0].Address != u.config.Address {
		log.Printf("Orphaned block %v:%v for coinbase address[%s] mismatch error", candidate.RoundHeight, candidate.Nonce, block.Transactions[0].Outputs[0].Address)
		return false
	}

	// Just compare hash if block is unlocked as immature
	if len(candidate.Hash) > 0 {
		if !strings.EqualFold(candidate.Hash, block.Hash) {
			return false
		}
	}
	
	nonce1, _ := strconv.ParseInt(block.Nonce, 10, 64)
	nonce2, _ := strconv.ParseInt(strings.Replace(candidate.Nonce, "0x", "", -1), 16, 64)

	return nonce1 == nonce2 
}

func (u *BlockUnlocker) handleBlock(block *rpc.GetBlockReply, candidate *storage.BlockData) error {
	correctHeight := block.Number
	candidate.Height = correctHeight
	reward := getConstReward(candidate.Height)
	
	// Add TX fees
	extraTxReward, err := u.getRewardWithFee(block)
	if err != nil {
		return fmt.Errorf("Error while fetching TX receipt: %v", err)
	}

	if extraTxReward.Cmp(reward) < 0  {
		return fmt.Errorf("extraTxReward[%s] must be >= reward[%s]", extraTxReward, reward)
	}

	extraTxReward.Sub(extraTxReward, reward)
	

	if u.config.KeepTxFees {
		candidate.ExtraReward = extraTxReward
	} else {
		reward.Add(reward, extraTxReward)
	}

	// Add reward for including uncles
	//uncleReward := getRewardForUncle(candidate.Height)
	//rewardForUncles := big.NewInt(0).Mul(uncleReward, big.NewInt(int64(len(block.Uncles))))
	//reward.Add(reward, rewardForUncles)

	candidate.Orphan = false
	candidate.Hash = block.Hash
	candidate.Reward = reward
	return nil
}

func (u *BlockUnlocker) unlockPendingBlocks() {
	if u.halt {
		log.Println("Unlocking suspended due to last critical error:", u.lastFail)
		return
	}

	current, err := u.rpc.GetPendingBlock()
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Unable to get current blockchain height from node1: %v", err)
		return
	}
	//currentHeight, err := strconv.ParseInt(strings.Replace(current.Number, "0x", "", -1), 16, 64)
	currentHeight, err := int64(current.Number), nil
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Can't parse pending block number: %v", err)
		return
	}

	candidates, err := u.backend.GetCandidates(currentHeight - u.config.ImmatureDepth)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to get block candidates from backend: %v", err)
		return
	}

	if len(candidates) == 0 {
		log.Println("No block candidates to unlock")
		return
	}

	result, err := u.unlockCandidates(candidates)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to unlock blocks: %v", err)
		return
	}
	log.Printf("Immature %v blocks, %v uncles, %v orphans", result.blocks, result.uncles, result.orphans)

	err = u.backend.WritePendingOrphans(result.orphanedBlocks)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to insert orphaned blocks into backend: %v", err)
		return
	} else {
		log.Printf("Inserted %v orphaned blocks to backend", result.orphans)
	}

	totalRevenue := new(big.Rat)
	totalMinersProfit := new(big.Rat)
	totalPoolProfit := new(big.Rat)

	for _, block := range result.maturedBlocks {
		revenue, minersProfit, poolProfit, roundRewards, err := u.calculateRewards(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to calculate rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		//log.Printf("backend WriteImmatureBlock, roundRewards=%s, Height=%d, Reward=%d, RewardString=%s", roundRewards, block.Height, *block.Reward, block.RewardString)
		err = u.backend.WriteImmatureBlock(block, roundRewards)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to credit rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		totalRevenue.Add(totalRevenue, revenue)
		totalMinersProfit.Add(totalMinersProfit, minersProfit)
		totalPoolProfit.Add(totalPoolProfit, poolProfit)

		logEntry := fmt.Sprintf(
			"IMMATURE %v: revenue %v, miners profit %v, pool profit: %v",
			block.RoundKey(),
			util.FormatRatReward(revenue),
			util.FormatRatReward(minersProfit),
			util.FormatRatReward(poolProfit),
		)
		entries := []string{logEntry}
		for login, reward := range roundRewards {
			entries = append(entries, fmt.Sprintf("\tREWARD %v: %v: %v Satoshi", block.RoundKey(), login, reward))
		}
		log.Println(strings.Join(entries, "\n"))
	}

	log.Printf(
		"IMMATURE SESSION: revenue %v, miners profit %v, pool profit: %v",
		util.FormatRatReward(totalRevenue),
		util.FormatRatReward(totalMinersProfit),
		util.FormatRatReward(totalPoolProfit),
	)
}

func (u *BlockUnlocker) unlockAndCreditMiners() {
	if u.halt {
		log.Println("Unlocking suspended due to last critical error:", u.lastFail)
		return
	}

	current, err := u.rpc.GetPendingBlock()
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Unable to get current blockchain height from node2: %v", err)
		return
	}
	//currentHeight, err := strconv.ParseInt(strings.Replace(current.Number, "0x", "", -1), 16, 64)
	currentHeight, err := int64(current.Number), nil//strconv.ParseInt(current.Number, 10, 64)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Can't parse pending block number: %v", err)
		return
	}

	immature, err := u.backend.GetImmatureBlocks(currentHeight - u.config.Depth)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to get block candidates from backend: %v", err)
		return
	}

	if len(immature) == 0 {
		log.Println("No immature blocks to credit miners")
		return
	}

	result, err := u.unlockCandidates(immature)
	if err != nil {
		u.halt = true
		u.lastFail = err
		log.Printf("Failed to unlock blocks: %v", err)
		return
	}
	log.Printf("Unlocked %v blocks, %v uncles, %v orphans", result.blocks, result.uncles, result.orphans)

	for _, block := range result.orphanedBlocks {
		err = u.backend.WriteOrphan(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to insert orphaned block into backend: %v", err)
			return
		}
	}
	log.Printf("Inserted %v orphaned blocks to backend", result.orphans)

	totalRevenue := new(big.Rat)
	totalMinersProfit := new(big.Rat)
	totalPoolProfit := new(big.Rat)

	for _, block := range result.maturedBlocks {
		revenue, minersProfit, poolProfit, roundRewards, err := u.calculateRewards(block)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to calculate rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		err = u.backend.WriteMaturedBlock(block, roundRewards)
		if err != nil {
			u.halt = true
			u.lastFail = err
			log.Printf("Failed to credit rewards for round %v: %v", block.RoundKey(), err)
			return
		}
		totalRevenue.Add(totalRevenue, revenue)
		totalMinersProfit.Add(totalMinersProfit, minersProfit)
		totalPoolProfit.Add(totalPoolProfit, poolProfit)

		logEntry := fmt.Sprintf(
			"MATURED %v: revenue %v, miners profit %v, pool profit: %v",
			block.RoundKey(),
			util.FormatRatReward(revenue),
			util.FormatRatReward(minersProfit),
			util.FormatRatReward(poolProfit),
		)
		entries := []string{logEntry}
		for login, reward := range roundRewards {
			entries = append(entries, fmt.Sprintf("\tREWARD %v: %v: %v Satoshi", block.RoundKey(), login, reward))
		}
		log.Println(strings.Join(entries, "\n"))
	}

	log.Printf(
		"MATURE SESSION: revenue %v, miners profit %v, pool profit: %v",
		util.FormatRatReward(totalRevenue),
		util.FormatRatReward(totalMinersProfit),
		util.FormatRatReward(totalPoolProfit),
	)
}

func (u *BlockUnlocker) calculateRewards(block *storage.BlockData) (*big.Rat, *big.Rat, *big.Rat, map[string]int64, error) {
	revenue := new(big.Rat).SetInt(block.Reward)
	minersProfit, poolProfit := chargeFee(revenue, u.config.PoolFee)

	shares, err := u.backend.GetRoundShares(block.RoundHeight, block.Nonce)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	rewards := calculateRewardsForShares(shares, block.TotalShares, minersProfit)

	if block.ExtraReward != nil {
		extraReward := new(big.Rat).SetInt(block.ExtraReward)
		poolProfit.Add(poolProfit, extraReward)
		revenue.Add(revenue, extraReward)
	}

	if u.config.Donate {
		var donation = new(big.Rat)
		poolProfit, donation = chargeFee(poolProfit, donationFee)
		login := strings.ToLower(donationAccount)
		fee, _ := strconv.ParseInt(donation.FloatString(0), 10, 64)
		rewards[login] += fee
	}

	if len(u.config.PoolFeeAddress) != 0 {
		fee, _ := strconv.ParseInt(poolProfit.FloatString(0), 10, 64)
		rewards[u.config.PoolFeeAddress] += fee
	}

	return revenue, minersProfit, poolProfit, rewards, nil
}

func calculateRewardsForShares(shares map[string]int64, total int64, reward *big.Rat) map[string]int64 {
	rewards := make(map[string]int64)

	for login, n := range shares {
		percent := big.NewRat(n, total)
		workerReward := new(big.Rat).Mul(reward, percent)
		fee, _ := strconv.ParseInt(workerReward.FloatString(0), 10, 64)
		rewards[login] += fee
	}
	return rewards
}

// Returns new value after fee deduction and fee value.
func chargeFee(value *big.Rat, fee float64) (*big.Rat, *big.Rat) {
	feePercent := new(big.Rat).SetFloat64(fee / 100)
	feeValue := new(big.Rat).Mul(value, feePercent)
	return new(big.Rat).Sub(value, feeValue), feeValue
}

func getConstReward(height int64) *big.Int {
	return big.NewInt(int64(300000000 * _math.Pow(0.95 , float64(height/500000))))
}

func (u *BlockUnlocker) getRewardWithFee(block *rpc.GetBlockReply) (*big.Int, error) {
	if len(block.Transactions[0].Outputs) != 1 {
		return nil, fmt.Errorf("coinbase invalid output length")
	}

	return big.NewInt( block.Transactions[0].Outputs[0].Value ), nil
}
