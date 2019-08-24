package web

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/Tinachain/Tina/chain/boker/agent/business"
	log4plus "github.com/Tinachain/Tina/chain/boker/common/log4go"
)

//****************Block Interface
type blockNumberResponse struct {
	Number uint64 `json:"number"`
	Timer  uint64 `json:"timer"`
}

func BlockgetBlockNumber(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	log4plus.Info("blockRouter.go BlockgetBlockNumber")

	if business.ChainClient == nil {
		log4plus.Error("blockRouter.go BlockgetBlockNumber Failed chainclient is nil")
	}

	log4plus.Info("blockRouter.go BlockgetBlockNumber chainclient GetBlockNumber")
	block, err := business.ChainClient.GetBlockNumber()
	if err != nil {

		log4plus.Error("blockRouter.go BlockgetBlockNumber chainclient GetBlockNumber is Failed")
		bytes, _ := json.Marshal(&ResponseCommon{0, ""})
		w.Write(bytes)
	}

	log4plus.Info("blockRouter.go BlockgetBlockNumber response GetBlockNumber")
	resp := &blockNumberResponse{}
	resp.Number = block.Number
	resp.Timer = block.Timer

	bytes, _ := json.Marshal(resp)
	w.Write(bytes)
}

type txRequest struct {
	Hash string `json:"hash"`
}
type txResponse struct {
	Major        uint64 `json:"major"`      //主交易类型
	Minor        uint64 `json:"minor"`      //次交易类型
	AccountNonce uint64 `json:"nonce"`      //交易Nonce
	Price        uint64 `json:"gasPrice"`   //Gas单价
	GasLimit     uint64 `json:"gas"`        //GasLimit
	From         string `json:"from"`       //交易发起方地址
	To           string `json:"to"`         //接收地址，可以为nil
	Amount       uint64 `json:"value"`      //交易使用的数量
	Payload      []byte `json:"input"`      //交易可以携带的数据
	Name         []byte `json:"name"`       //文件名称，这个文件名称只有在扩展类型中的图片类型和文件类型时启作用。
	Encryption   uint8  `json:"encryption"` //扩展数据是否已经加密
	Extra        []byte `json:"extra"`      //扩展数据
	Time         uint64 `json:"timestamp"`  //交易发起时间
	Ip           []byte `json:"ip"`         //交易提交的IP信息
	Pending      bool   `json:"pending"`    //交易是否Pending
}

func BlockgetTx(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	log4plus.Info("blockRouter.go BlockgetTx")

	if business.ChainClient == nil {
		log4plus.Error("blockRouter.go BlockgetTx Failed chainclient is nil")
	}

	bodyBytes, _ := ioutil.ReadAll(r.Body)
	req := &txRequest{}
	err := json.Unmarshal(bodyBytes, req)

	if err != nil {

		log4plus.Error("blockRouter.go BlockgetTx", "err", err)
		HttpError(w, -1, err.Error())
		return
	}

	log4plus.Info("blockRouter.go BlockgetTx chainclient GetTx hash=%s", req.Hash)
	tx, pinding, err := business.ChainClient.GetTx(req.Hash)
	if err != nil {

		log4plus.Error("blockRouter.go BlockgetTx chainclient GetTx is Failed")
		bytes, _ := json.Marshal(&ResponseCommon{0, ""})
		w.Write(bytes)
	}

	resp := &txResponse{}
	resp.Major = uint64(tx.Major())
	resp.Minor = uint64(tx.Minor())
	resp.AccountNonce = tx.Nonce()
	resp.Price = tx.GasPrice().Uint64()
	resp.GasLimit = tx.Gas().Uint64()
	resp.To = tx.To().String()
	resp.Amount = tx.Value().Uint64()
	copy(resp.Payload[:], tx.Data()[:])
	copy(resp.Name[:], tx.Name()[:])
	resp.Encryption = tx.Encryption()
	copy(resp.Name[:], tx.Name()[:])
	copy(resp.Extra[:], tx.Extra()[:])
	resp.Time = tx.Time().Uint64()
	copy(resp.Ip[:], tx.Ip()[:])
	resp.Pending = pinding

	bytes, _ := json.Marshal(resp)
	w.Write(bytes)
}
