package models

import (
	"fmt"
	"multi-chain-storage/database"

	"github.com/filswan/go-swan-lib/logs"
)

type Wallet struct {
	ID       int64  `json:"id"`
	Address  string `json:"address"`
	Type     string `json:"type"`
	CreateAt int64  `json:"create_at"`
}

func GeWalletByAddressType(address string, walletType int) (*Wallet, error) {
	var wallets []*Wallet
	err := database.GetDB().Where("address=? and type=?", address, walletType).Find(&wallets).Error
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	if len(wallets) > 0 {
		return wallets[0], nil
	}

	err = fmt.Errorf("no wallet for address:%s,type:%d", address, walletType)
	logs.GetLogger().Error(err)

	return nil, err
}