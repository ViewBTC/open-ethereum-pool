package util

import (
	"math/big"
	"regexp"
	"strconv"
	"time"
	"bytes"
	"crypto/sha256"
	"reflect"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
)

var Ether = math.BigPow(10, 18)
var Satoshi = math.BigPow(10, 8)

var pow256 = math.BigPow(2, 256)
var bitcoinAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
var b58Alphabet = []byte(bitcoinAlphabet)
var addressPattern = regexp.MustCompile("^M[" + bitcoinAlphabet + "]{33}$")
var zeroHash = regexp.MustCompile("^0?x?0+$")

func Base58Decode(input []byte) []byte {
    result := big.NewInt(0)
    zeroBytes := 0

    for _, b := range input {
        if b != b58Alphabet[0] {
            break
        }

        zeroBytes++
    }

    payload := input[zeroBytes:]
    for _, b := range payload {
        charIndex := bytes.IndexByte(b58Alphabet, b)
        result.Mul(result, big.NewInt(int64(len(b58Alphabet))))
        result.Add(result, big.NewInt(int64(charIndex)))
    }

    decoded := result.Bytes()
    decoded = append(bytes.Repeat([]byte{byte(0x00)}, zeroBytes), decoded...)

    return decoded
}

func IsValidBitcoinAddress(s string) bool {
	if !addressPattern.MatchString(s) {
		return false
	}

	decoded := Base58Decode([]byte(s))

	bitcoin_hash := func (input []byte) []byte {
		h1 := sha256.New()
		h1.Write(input)

		h2 := sha256.New()
		h2.Write(h1.Sum(nil))
		return h2.Sum(nil)
	}

	checksum := bitcoin_hash(decoded[:len(decoded)-4])


	return reflect.DeepEqual(checksum[:4], decoded[len(decoded)-4:])
}

func IsZeroHash(s string) bool {
	return zeroHash.MatchString(s)
}

func MakeTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func GetTargetHex(diff int64) string {
	difficulty := big.NewInt(diff)
	diff1 := new(big.Int).Div(pow256, difficulty)
	return string(common.ToHex(diff1.Bytes()))
}

func TargetHexToDiff(targetHex string) *big.Int {
	targetBytes := common.FromHex(targetHex)
	return new(big.Int).Div(pow256, new(big.Int).SetBytes(targetBytes))
}

func ToHex(n int64) string {
	return "0x0" + strconv.FormatInt(n, 16)
}

func FormatReward(reward *big.Int) string {
	return reward.String()
}

func FormatRatReward(reward *big.Rat) string {
	wei := new(big.Rat).SetInt(Ether)
	reward = reward.Quo(reward, wei)
	return reward.FloatString(8)
}

func StringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func MustParseDuration(s string) time.Duration {
	value, err := time.ParseDuration(s)
	if err != nil {
		panic("util: Can't parse duration `" + s + "`: " + err.Error())
	}
	return value
}

func String2Big(num string) *big.Int {
	n := new(big.Int)
	n.SetString(num, 0)
	return n
}
