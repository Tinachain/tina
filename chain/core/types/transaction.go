// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package types

import (
	"container/heap"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/Tinachain/Tina/chain/boker/protocol"
	"github.com/Tinachain/Tina/chain/common"
	"github.com/Tinachain/Tina/chain/common/hexutil"
	"github.com/Tinachain/Tina/chain/crypto"
	"github.com/Tinachain/Tina/chain/rlp"
)

//go:generate gencodec -type txdata -field-override txdataMarshaling -out gen_tx_json.go

var (
	ErrInvalidSig = errors.New("invalid transaction v, r, s values")
)

// deriveSigner makes a *best* guess about which signer to use.
func deriveSigner(V *big.Int) Signer {
	if V.Sign() != 0 && isProtectedV(V) {
		return NewEIP155Signer(deriveChainId(V))
	} else {
		return HomesteadSigner{}
	}
}

type Transaction struct {
	data txdata
	hash atomic.Value
	size atomic.Value
	from atomic.Value
}

//这里注意算法 交易费 = gasUsed * gasPrice
type txdata struct {
	Major        protocol.TxMajor `json:"major"   gencodec:"required"`          //主交易类型
	Minor        protocol.TxMinor `json:"minor"   gencodec:"required"`          //次交易类型
	AccountNonce uint64           `json:"nonce"    gencodec:"required"`         //交易Nonce
	Price        *big.Int         `json:"gasPrice" gencodec:"required"`         //Gas单价
	GasLimit     *big.Int         `json:"gas"      gencodec:"required"`         //GasLimit
	Time         *big.Int         `json:"timestamp"        gencodec:"required"` //交易发起时间
	Recipient    *common.Address  `json:"to"       rlp:"nil"`                   //接收地址，可以为nil
	Amount       *big.Int         `json:"value"    gencodec:"required"`         //交易使用的数量
	Payload      []byte           `json:"input"    gencodec:"required"`         //交易可以携带的数据，在不同类型的交易中有不同的含义(这个字段在eth.sendTransaction()中对应的是data字段，在eth.getTransaction()中对应的是input字段)
	Name         []byte           `json:"name"    gencodec:"required"`          //文件名称，这个文件名称只有在扩展类型中的图片类型和文件类型时启作用。
	Extra        []byte           `json:"extra"    gencodec:"required"`         //扩展数据
	Ip           []byte           `json:"ip"    gencodec:"required"`            //交易提交的IP信息

	//交易的签名数据
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`

	// This is only used when marshaling to JSON.
	Hash *common.Hash `json:"hash" rlp:"-"`
}

type txdataMarshaling struct {
	AccountNonce hexutil.Uint64
	Price        *hexutil.Big
	GasLimit     *hexutil.Big
	Amount       *hexutil.Big
	Name         hexutil.Bytes
	Payload      hexutil.Bytes
	Extra        hexutil.Bytes
	Major        protocol.TxMajor
	Minor        protocol.TxMinor
	Ip           hexutil.Bytes
	V            *hexutil.Big
	R            *hexutil.Big
	S            *hexutil.Big
}

//创建交易
func NewTransaction(txMajor protocol.TxMajor, txMinor protocol.TxMinor, nonce uint64, to common.Address, amount, gasLimit, gasPrice *big.Int, payload []byte) *Transaction {
	return newTransaction(txMajor, txMinor, nonce, &to, amount, gasLimit, gasPrice, payload)
}

//创建基础交易
func NewBaseTransaction(txMajor protocol.TxMajor, txMinor protocol.TxMinor, nonce uint64, to common.Address, amount *big.Int, payload []byte) *Transaction {
	return newTransaction(txMajor, txMinor, nonce, &to, amount, protocol.MaxGasLimit, protocol.MaxGasPrice, payload)
}

//创建扩展交易
func NewExtraTransaction(txMajor protocol.TxMajor, txMinor protocol.TxMinor, nonce uint64, to common.Address, amount, gasLimit, gasPrice *big.Int, name []byte, extra []byte) *Transaction {

	//判断数据是否长度大于0
	if len(extra) > 0 {
		extra = common.CopyBytes(extra)
	}

	//构造一个交易结构(注意这里的txType类型和Gas的关系)
	d := txdata{
		AccountNonce: nonce,
		Recipient:    &to,
		Amount:       new(big.Int),
		GasLimit:     new(big.Int),
		Time:         new(big.Int),
		Price:        new(big.Int),
		Major:        txMajor,
		Minor:        txMinor,
		V:            new(big.Int),
		R:            new(big.Int),
		S:            new(big.Int),
	}

	//设置扩展字段
	if txMajor == protocol.Extra {

		d.Extra = d.Extra[:0]
		d.Extra = append(d.Extra, extra...)

		d.Name = d.Name[:0]
		d.Name = append(d.Name, name...)
	}

	//设置交易时间
	d.Time.SetInt64(time.Now().Unix())

	if amount != nil {
		d.Amount.Set(amount)
	}
	if gasLimit != nil {
		d.GasLimit.Set(gasLimit)
	}
	if gasPrice != nil {
		d.Price.Set(gasPrice)
	}
	//得到当前生成区块的公网IP
	Ip := protocol.GetExternalIp()
	d.Ip = d.Ip[:0]
	d.Ip = append(d.Ip, Ip...)

	return &Transaction{data: d}
}

//创建合约
func NewContractCreation(nonce uint64, amount, gasLimit, gasPrice *big.Int, payload []byte) *Transaction {
	return newTransaction(protocol.Normal, 0, nonce, nil, amount, gasLimit, gasPrice, payload)
}

func newTransaction(txMajor protocol.TxMajor, txMinor protocol.TxMinor, nonce uint64, to *common.Address, amount, gasLimit, gasPrice *big.Int, payload []byte) *Transaction {

	//判断数据是否长度大于0
	if len(payload) > 0 {
		payload = common.CopyBytes(payload)
	}

	//构造一个交易结构(注意这里的txType类型和Gas的关系)
	d := txdata{
		AccountNonce: nonce,
		Recipient:    to,
		Payload:      payload,
		Amount:       new(big.Int),
		GasLimit:     new(big.Int),
		Time:         new(big.Int),
		Price:        new(big.Int),
		Major:        txMajor,
		Minor:        txMinor,
		V:            new(big.Int),
		R:            new(big.Int),
		S:            new(big.Int),
	}

	//设置交易时间
	d.Time.SetInt64(time.Now().Unix())

	if amount != nil {
		d.Amount.Set(amount)
	}
	if gasLimit != nil {
		d.GasLimit.Set(gasLimit)
	}
	if gasPrice != nil {
		d.Price.Set(gasPrice)
	}

	//得到当前生成区块的公网IP
	Ip := protocol.GetExternalIp()
	d.Ip = d.Ip[:0]
	d.Ip = append(d.Ip, Ip...)

	return &Transaction{data: d}
}

// ChainId returns which chain id this transaction was signed for (if at all)
func (tx *Transaction) ChainId() *big.Int {
	return deriveChainId(tx.data.V)
}

func IsSetSystemContract(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.SetSystemContract {
		return true
	} else {
		return false
	}
}

func IsCancelSystemContract(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.CancelSystemContract {
		return true
	} else {
		return false
	}
}

func IsVoteUser(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.VoteUser {
		return true
	} else {
		return false
	}
}

func IsVoteEpoch(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.VoteEpoch {
		return true
	} else {
		return false
	}
}

func IsRegisterCandidate(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.RegisterCandidate {
		return true
	} else {
		return false
	}
}

func IsSetValidator(txMajor protocol.TxMajor, txMinor protocol.TxMinor) bool {

	if txMajor != protocol.Base {
		return false
	}

	if txMinor == protocol.SetValidator {
		return true
	} else {
		return false
	}
}

//判断是否是各种类型的合约
func IsNormal(txMajor protocol.TxMajor) bool {

	if txMajor == protocol.Normal {
		return true
	} else {
		return false
	}
}

//验证交易类型是否可知
func (tx *Transaction) Validate() error {

	if tx.Major() < protocol.Normal || tx.Major() > protocol.Extra {
		return errors.New("unknown major transaction type")
	}

	switch tx.Major() {

	case protocol.Base:
		{
			if tx.Minor() < protocol.MinMinor || tx.Minor() > protocol.MaxMinor {
				return errors.New("base transaction unknown minor transaction type")
			}
		}
	case protocol.Extra:
		{
			if tx.Minor() < protocol.Word || tx.Minor() > protocol.File {
				return errors.New("extra transaction unknown minor transaction type")
			}
		}
	}
	return nil
}

func (tx *Transaction) SetIp() error {

	Ip := protocol.GetExternalIp()
	tx.data.Ip = tx.data.Ip[:0]
	tx.data.Ip = append(tx.data.Ip, Ip...)

	return nil
}

// Protected returns whether the transaction is protected from replay protection.
func (tx *Transaction) Protected() bool {
	return isProtectedV(tx.data.V)
}

func isProtectedV(V *big.Int) bool {
	if V.BitLen() <= 8 {
		v := V.Uint64()
		return v != 27 && v != 28
	}
	// anything not 27 or 28 are considered unprotected
	return true
}

// DecodeRLP implements rlp.Encoder
func (tx *Transaction) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &tx.data)
}

// DecodeRLP implements rlp.Decoder
func (tx *Transaction) DecodeRLP(s *rlp.Stream) error {
	_, size, _ := s.Kind()
	err := s.Decode(&tx.data)
	if err == nil {
		tx.size.Store(common.StorageSize(rlp.ListSize(size)))
	}

	return err
}

func (tx *Transaction) MarshalJSON() ([]byte, error) {
	hash := tx.Hash()
	data := tx.data
	data.Hash = &hash
	return data.MarshalJSON()
}

// UnmarshalJSON decodes the web3 RPC transaction format.
func (tx *Transaction) UnmarshalJSON(input []byte) error {
	var dec txdata
	if err := dec.UnmarshalJSON(input); err != nil {
		return err
	}
	var V byte
	if isProtectedV(dec.V) {
		chainId := deriveChainId(dec.V).Uint64()
		V = byte(dec.V.Uint64() - 35 - 2*chainId)
	} else {
		V = byte(dec.V.Uint64() - 27)
	}
	if !crypto.ValidateSignatureValues(V, dec.R, dec.S, false) {
		return ErrInvalidSig
	}
	*tx = Transaction{data: dec}
	return nil
}

func (tx *Transaction) Data() []byte            { return common.CopyBytes(tx.data.Payload) }
func (tx *Transaction) Name() []byte            { return common.CopyBytes(tx.data.Name) }
func (tx *Transaction) Extra() []byte           { return common.CopyBytes(tx.data.Extra) }
func (tx *Transaction) Gas() *big.Int           { return new(big.Int).Set(tx.data.GasLimit) }
func (tx *Transaction) GasPrice() *big.Int      { return new(big.Int).Set(tx.data.Price) }
func (tx *Transaction) Value() *big.Int         { return new(big.Int).Set(tx.data.Amount) }
func (tx *Transaction) Nonce() uint64           { return tx.data.AccountNonce }
func (tx *Transaction) CheckNonce() bool        { return true }
func (tx *Transaction) Major() protocol.TxMajor { return tx.data.Major }
func (tx *Transaction) Minor() protocol.TxMinor { return tx.data.Minor }
func (tx *Transaction) Time() *big.Int          { return tx.data.Time }
func (tx *Transaction) Ip() []byte              { return common.CopyBytes(tx.data.Ip) }

// To returns the recipient address of the transaction.
// It returns nil if the transaction is a contract creation.
func (tx *Transaction) To() *common.Address {
	if tx.data.Recipient == nil {
		return nil
	} else {
		to := *tx.data.Recipient
		return &to
	}
}

// Hash hashes the RLP encoding of tx.
// It uniquely identifies the transaction.
func (tx *Transaction) Hash() common.Hash {
	if hash := tx.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}
	v := rlpHash(tx)
	tx.hash.Store(v)
	return v
}

func (tx *Transaction) Size() common.StorageSize {
	if size := tx.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, &tx.data)
	tx.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

// AsMessage returns the transaction as a core.Message.
//
// AsMessage requires a signer to derive the sender.
//
// XXX Rename message to something less arbitrary?
func (tx *Transaction) AsMessage(s Signer) (Message, error) {
	msg := Message{
		nonce:      tx.data.AccountNonce,
		price:      new(big.Int).Set(tx.data.Price),
		gasLimit:   new(big.Int).Set(tx.data.GasLimit),
		to:         tx.data.Recipient,
		amount:     tx.data.Amount,
		data:       tx.data.Payload,
		name:       tx.data.Name,
		extra:      tx.data.Extra,
		major:      tx.data.Major,
		minor:      tx.data.Minor,
		ip:         tx.data.Ip,
		checkNonce: true,
	}

	var err error
	msg.from, err = Sender(s, tx)
	return msg, err
}

// WithSignature returns a new transaction with the given signature.
// This signature needs to be formatted as described in the yellow paper (v+27).
func (tx *Transaction) WithSignature(signer Signer, sig []byte) (*Transaction, error) {
	r, s, v, err := signer.SignatureValues(tx, sig)
	if err != nil {
		return nil, err
	}
	cpy := &Transaction{data: tx.data}
	cpy.data.R, cpy.data.S, cpy.data.V = r, s, v
	return cpy, nil
}

//返回本次交易的最大成本 = Value + Price * GasLimit
func (tx *Transaction) Cost() *big.Int {
	total := new(big.Int).Mul(tx.data.Price, tx.data.GasLimit)
	total.Add(total, tx.data.Amount)
	return total
}

func (tx *Transaction) RawSignatureValues() (*big.Int, *big.Int, *big.Int) {
	return tx.data.V, tx.data.R, tx.data.S
}

func (tx *Transaction) String() string {
	var from, to string
	if tx.data.V != nil {
		// make a best guess about the signer and use that to derive
		// the sender.
		signer := deriveSigner(tx.data.V)
		if f, err := Sender(signer, tx); err != nil { // derive but don't cache
			from = "[invalid sender: invalid sig]"
		} else {
			from = fmt.Sprintf("%x", f[:])
		}
	} else {
		from = "[invalid sender: nil V field]"
	}

	if tx.data.Recipient == nil {
		to = "[contract creation]"
	} else {
		to = fmt.Sprintf("%x", tx.data.Recipient[:])
	}
	enc, _ := rlp.EncodeToBytes(&tx.data)
	return fmt.Sprintf(`
	TX(%x)
	Major:	  %d
	Minor: 	%d
	Contract: %v
	From:     %s
	To:       %s
	Nonce:    %v
	GasPrice: %#x
	GasLimit  %#x
	Value:    %#x
	Name:		0x%x
	Data:     0x%x
	Extra:	 0x%x
	Ip:			%s
	V:        %#x
	R:        %#x
	S:        %#x
	Hex:      %x
`,
		tx.Hash(),
		tx.Major(),
		tx.Minor(),
		tx.data.Recipient == nil,
		from,
		to,
		tx.data.AccountNonce,
		tx.data.Price,
		tx.data.GasLimit,
		tx.data.Amount,
		tx.data.Name,
		tx.data.Payload,
		tx.data.Extra,
		string(tx.data.Ip[:]),
		tx.data.V,
		tx.data.R,
		tx.data.S,
		enc,
	)
}

// Transaction slice type for basic sorting.
type Transactions []*Transaction

// Len returns the length of s
func (s Transactions) Len() int { return len(s) }

// Swap swaps the i'th and the j'th element in s
func (s Transactions) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// GetRlp implements Rlpable and returns the i'th element of s in rlp
func (s Transactions) GetRlp(i int) []byte {
	enc, _ := rlp.EncodeToBytes(s[i])
	return enc
}

// Returns a new set t which is the difference between a to b
func TxDifference(a, b Transactions) (keep Transactions) {
	keep = make(Transactions, 0, len(a))

	remove := make(map[common.Hash]struct{})
	for _, tx := range b {
		remove[tx.Hash()] = struct{}{}
	}

	for _, tx := range a {
		if _, ok := remove[tx.Hash()]; !ok {
			keep = append(keep, tx)
		}
	}

	return keep
}

// TxByNonce implements the sort interface to allow sorting a list of transactions
// by their nonces. This is usually only useful for sorting transactions from a
// single account, otherwise a nonce comparison doesn't make much sense.
type TxByNonce Transactions

func (s TxByNonce) Len() int           { return len(s) }
func (s TxByNonce) Less(i, j int) bool { return s[i].data.AccountNonce < s[j].data.AccountNonce }
func (s TxByNonce) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// TxByPrice implements both the sort and the heap interface, making it useful
// for all at once sorting as well as individually adding and removing elements.
type TxByPrice Transactions

func (s TxByPrice) Len() int           { return len(s) }
func (s TxByPrice) Less(i, j int) bool { return s[i].data.Price.Cmp(s[j].data.Price) > 0 }
func (s TxByPrice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (s *TxByPrice) Push(x interface{}) {
	*s = append(*s, x.(*Transaction))
}

func (s *TxByPrice) Pop() interface{} {
	old := *s
	n := len(old)
	x := old[n-1]
	*s = old[0 : n-1]
	return x
}

// TransactionsByPriceAndNonce represents a set of transactions that can return
// transactions in a profit-maximising sorted order, while supporting removing
// entire batches of transactions for non-executable accounts.
type TransactionsByPriceAndNonce struct {
	txs    map[common.Address]Transactions // Per account nonce-sorted list of transactions
	heads  TxByPrice                       // Next transaction for each unique account (price heap)
	signer Signer                          // Signer for the set of transactions
}

//创建一个可以检索的交易集
func NewTransactionsByPriceAndNonce(signer Signer, txs map[common.Address]Transactions) *TransactionsByPriceAndNonce {

	// Initialize a price based heap with the head transactions
	heads := make(TxByPrice, 0, len(txs))
	for _, accTxs := range txs {
		heads = append(heads, accTxs[0])
		// Ensure the sender address is from the signer
		acc, _ := Sender(signer, accTxs[0])
		txs[acc] = accTxs[1:]
	}
	heap.Init(&heads)

	// Assemble and return the transaction set
	return &TransactionsByPriceAndNonce{
		txs:    txs,
		heads:  heads,
		signer: signer,
	}
}

// Peek returns the next transaction by price.
func (t *TransactionsByPriceAndNonce) Peek() *Transaction {
	if len(t.heads) == 0 {
		return nil
	}
	return t.heads[0]
}

// Shift replaces the current best head with the next one from the same account.
func (t *TransactionsByPriceAndNonce) Shift() {
	acc, _ := Sender(t.signer, t.heads[0])
	if txs, ok := t.txs[acc]; ok && len(txs) > 0 {
		t.heads[0], t.txs[acc] = txs[0], txs[1:]
		heap.Fix(&t.heads, 0)
	} else {
		heap.Pop(&t.heads)
	}
}

// Pop removes the best transaction, *not* replacing it with the next one from
// the same account. This should be used when a transaction cannot be executed
// and hence all subsequent ones should be discarded from the same account.
func (t *TransactionsByPriceAndNonce) Pop() {
	heap.Pop(&t.heads)
}

// Message is a fully derived transaction and implements core.Message
//
// NOTE: In a future PR this will be removed.
type Message struct {
	to                      *common.Address
	from                    common.Address
	nonce                   uint64
	amount, price, gasLimit *big.Int
	name                    []byte
	data                    []byte
	extra                   []byte
	checkNonce              bool
	major                   protocol.TxMajor
	minor                   protocol.TxMinor
	ip                      []byte
}

func NewMessage(from common.Address,
	to *common.Address,
	nonce uint64,
	amount, gasLimit, price *big.Int,
	name []byte,
	data []byte,
	extra []byte,
	ip []byte,
	checkNonce bool,
	major protocol.TxMajor,
	minor protocol.TxMinor) Message {
	return Message{
		from:       from,
		to:         to,
		nonce:      nonce,
		amount:     amount,
		price:      price,
		gasLimit:   gasLimit,
		name:       name,
		data:       data,
		extra:      extra,
		checkNonce: checkNonce,
		major:      major,
		minor:      minor,
		ip:         ip,
	}
}

func (m Message) From() common.Address    { return m.from }
func (m Message) To() *common.Address     { return m.to }
func (m Message) GasPrice() *big.Int      { return m.price }
func (m Message) Value() *big.Int         { return m.amount }
func (m Message) Gas() *big.Int           { return m.gasLimit }
func (m Message) Nonce() uint64           { return m.nonce }
func (m Message) Name() []byte            { return m.name }
func (m Message) Data() []byte            { return m.data }
func (m Message) Extra() []byte           { return m.extra }
func (m Message) CheckNonce() bool        { return m.checkNonce }
func (m Message) Major() protocol.TxMajor { return m.major }
func (m Message) Minor() protocol.TxMinor { return m.minor }
func (m Message) Ip() []byte              { return m.ip }
