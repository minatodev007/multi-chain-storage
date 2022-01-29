package scheduler

import (
	"context"
	"fmt"
	"math/big"
	"payment-bridge/blockchain/browsersync/scanlockpayment/polygon"
	"payment-bridge/common/constants"
	"payment-bridge/common/utils"
	"payment-bridge/config"
	"payment-bridge/database"
	"payment-bridge/models"
	"payment-bridge/on-chain/goBind"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	common2 "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/filswan/go-swan-lib/logs"
	"github.com/robfig/cron"
)

func CreateUnlockScheduler() {
	Mutex := &sync.Mutex{}
	c := cron.New()
	err := c.AddFunc(config.GetConfig().ScheduleRule.UnlockPaymentRule, func() {
		logs.GetLogger().Info("^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ create task  scheduler is running at " + time.Now().Format("2006-01-02 15:04:05"))
		Mutex.Lock()
		err := UnlockPayment()
		Mutex.Unlock()
		if err != nil {
			logs.GetLogger().Error(err)
			return
		}
	})
	if err != nil {
		logs.GetLogger().Error(err)
		return
	}
	c.Start()
}

func UnlockPayment() error {
	offlineDeals, err := models.GetOfflineDeals2BeUnlocked()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if len(offlineDeals) == 0 {
		logs.GetLogger().Info("no deal to be unlocked")
		return nil
	}

	adminAddress := common.HexToAddress(config.GetConfig().AdminWalletOnPolygon)

	ethClient, err := DialEthClient()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	nonce, err := ethClient.PendingNonceAt(context.Background(), adminAddress)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	gasPrice, err := ethClient.SuggestGasPrice(context.Background())
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	privateKey, err := crypto.HexToECDSA(privateKeyOnPolygon)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	chainId, err := ethClient.ChainID(context.Background())
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	callOpts, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	callOpts.Nonce = big.NewInt(int64(nonce))
	callOpts.GasPrice = gasPrice
	callOpts.GasLimit = uint64(polygon.GetConfig().PolygonMainnetNode.GasLimit)
	callOpts.Context = context.Background()

	recipient := common.HexToAddress(polygon.GetConfig().PolygonMainnetNode.PaymentContractAddress)
	swanPaymentTransactor, err := goBind.NewSwanPaymentTransactor(recipient, ethClient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	daoAddress := common2.HexToAddress(polygon.GetConfig().PolygonMainnetNode.DaoSwanOracleAddress)
	daoOracleContractInstance, err := goBind.NewFilswanOracle(daoAddress, ethClient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	filswanOracleSession := &goBind.FilswanOracleSession{}
	filswanOracleSession.Contract = daoOracleContractInstance

	for _, offlineDeal := range offlineDeals {
		err = unlock4Deal(filswanOracleSession, offlineDeal.Id, offlineDeal.DealId, offlineDeal.DealFileId, ethClient, swanPaymentTransactor, callOpts, recipient)
		if err != nil {
			logs.GetLogger().Error(err)
			continue
		}
	}
	return nil
}

func unlock4Deal(filswanOracleSession *goBind.FilswanOracleSession, offlineDealId, dealId, dealFileId int64, client *ethclient.Client, swanPaymentTransactor *goBind.SwanPaymentTransactor, callOpts *bind.TransactOpts, recipient common.Address) error {
	dealIdStr := strconv.FormatInt(dealId, 10)

	isPaymentAvailable, err := filswanOracleSession.IsCarPaymentAvailable(dealIdStr, recipient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if !isPaymentAvailable {
		msg := fmt.Sprintf("payment is not available for deal:%s,recipient:%s", dealIdStr, recipient)
		logs.GetLogger().Info(msg)
		return nil
	}

	tx, err := swanPaymentTransactor.UnlockCarPayment(callOpts, dealIdStr, recipient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	txReceipt, err := utils.CheckTx(client, tx)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if txReceipt.Status != uint64(1) {
		err := fmt.Errorf("unlock failed! txHash=" + tx.Hash().Hex())
		logs.GetLogger().Error(err)
		return err
	}

	unlockTxStatus := constants.TRANSACTION_STATUS_SUCCESS
	logs.GetLogger().Println("unlock success! txHash=" + tx.Hash().Hex())

	err = models.UpdateOfflineDealUnlockStatus(offlineDealId, constants.OFFLINE_DEAL_UNLOCK_STATUS_UNLOCKED)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if len(txReceipt.Logs) > 0 {
		eventLogs := txReceipt.Logs
		err = saveUnlockEventLogToDB(eventLogs, unlockTxStatus, dealId)
		if err != nil {
			logs.GetLogger().Error(err)
			return err
		}
	}

	offlineDealsNotUnlocked, err := models.GetOfflineDealsNotUnlockedByDealFileId(dealFileId)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if len(offlineDealsNotUnlocked) == 0 {
		var srcFilePayloadCids []string
		srcFiles, err := models.GetSourceFilesByDealFileId(dealFileId)
		if err != nil {
			logs.GetLogger().Error(err)
			return err
		}

		for _, srcFile := range srcFiles {
			srcFilePayloadCids = append(srcFilePayloadCids, srcFile.PayloadCid)
		}

		lockPaymentStatus := constants.LOCK_PAYMENT_STATUS_UNLOCK_REFUNDED
		_, err = swanPaymentTransactor.Refund(callOpts, srcFilePayloadCids)
		if err != nil {
			lockPaymentStatus = constants.LOCK_PAYMENT_STATUS_UNLOCK_REFUNDFAILED
			logs.GetLogger().Error(err)
		}

		currrentTime := utils.GetCurrentUtcMilliSecond()
		err = models.UpdateDealFile(models.DealFile{ID: dealFileId},
			map[string]interface{}{"lock_payment_status": lockPaymentStatus, "update_at": currrentTime})
		if err != nil {
			logs.GetLogger().Error(err)
		}
	}

	return nil
}

func saveUnlockEventLogToDB(logsInChain []*types.Log, unlockStatus string, dealId int64) error {
	paymentAbiString := goBind.SwanPaymentABI

	contractUnlockFunctionSignature := polygon.GetConfig().PolygonMainnetNode.ContractUnlockFunctionSignature
	contractAbi, err := abi.JSON(strings.NewReader(paymentAbiString))
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}
	for _, vLog := range logsInChain {
		//if log have this contractor function signer
		if vLog.Topics[0].Hex() == contractUnlockFunctionSignature {
			eventList, err := models.FindEventUnlockPayments(&models.EventUnlockPayment{TxHash: vLog.TxHash.Hex(), BlockNo: strconv.FormatUint(vLog.BlockNumber, 10)}, "id desc", "10", "0")
			if err != nil {
				logs.GetLogger().Error(err)
				continue
			}
			if len(eventList) <= 0 {
				event := new(models.EventUnlockPayment)
				dataList, err := contractAbi.Unpack("UnlockPayment", vLog.Data)
				if err != nil {
					logs.GetLogger().Error(err)
					continue
				}
				event.DealId = dealId
				event.TxHash = vLog.TxHash.Hex()
				networkId, err := models.FindNetworkIdByUUID(constants.NETWORK_TYPE_POLYGON_UUID)
				if err != nil {
					logs.GetLogger().Error(err)
				} else {
					event.NetworkId = networkId
				}
				coinId, err := models.FindCoinIdByUUID(constants.COIN_TYPE_USDC_ON_POLYGON_UUID)
				if err != nil {
					logs.GetLogger().Error(err)
				} else {
					event.CoinId = coinId
				}
				event.TokenAddress = dataList[1].(common.Address).Hex()
				event.UnlockToAdminAmount = dataList[2].(*big.Int).String()
				event.UnlockToUserAmount = dataList[3].(*big.Int).String()
				event.UnlockToAdminAddress = dataList[4].(common.Address).Hex()
				event.UnlockToUserAddress = dataList[5].(common.Address).Hex()
				event.UnlockTime = strconv.FormatInt(utils.GetCurrentUtcMilliSecond(), 10)
				event.BlockNo = strconv.FormatUint(vLog.BlockNumber, 10)
				event.CreateAt = utils.GetCurrentUtcMilliSecond()
				event.UnlockStatus = unlockStatus
				err = database.SaveOneWithTransaction(event)
				if err != nil {
					logs.GetLogger().Error(err)
				}

			}
		}
	}
	return nil
}
