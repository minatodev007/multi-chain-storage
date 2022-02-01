package client

import (
	"fmt"
	"payment-bridge/common/constants"
	"payment-bridge/common/utils"
	"payment-bridge/models"

	"github.com/filswan/go-swan-lib/logs"
)

func GetPaymentInfo(srcFilePayloadCid string) (*models.EventLockPayment, error) {
	swanPaymentSession, err := GetPaymentSession()
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	paymentInfo, err := swanPaymentSession.GetLockedPaymentInfo(srcFilePayloadCid)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	if !paymentInfo.IsExisted {
		err := fmt.Errorf("payment for source file with payload_cid:%s not exists", srcFilePayloadCid)
		logs.GetLogger().Error(err)
		return nil, err
	}

	var event *models.EventLockPayment
	if paymentInfo.IsExisted {
		event = new(models.EventLockPayment)
		event.Deadline = paymentInfo.Deadline.String()
		event.TokenAddress = paymentInfo.Token.Hex()
		event.AddressFrom = paymentInfo.Owner.String()
		event.AddressTo = paymentInfo.Recipient.String()
		event.LockedFee = paymentInfo.LockedFee.String()
		event.PayloadCid = srcFilePayloadCid
		event.LockPaymentTime = utils.GetCurrentUtcMilliSecond()
		coin, err := models.FindCoinByCoinAddress(event.TokenAddress)
		if err != nil {
			logs.GetLogger().Error(err)
		} else {
			event.CoinId = coin.ID
		}

		networkId, err := models.FindNetworkIdByUUID(constants.NETWORK_TYPE_POLYGON_UUID)
		if err != nil {
			logs.GetLogger().Error(err)
		} else {
			event.NetworkId = networkId
		}

		srcFile, err := models.GetSourceFileByPayloadCidWalletAddress(srcFilePayloadCid, event.AddressFrom)
		if err != nil {
			logs.GetLogger().Error(err)
		} else {
			event.SourceFileId = srcFile.ID
		}

		//err = database.SaveOne(event)
		//if err != nil {
		//	logs.GetLogger().Error(err)
		//	return nil, err
		//}
		//
		//dealFiles, err := models.GetDealFileBySourceFilePayloadCid(srcFilePayloadCid)
		//if err != nil {
		//	logs.GetLogger().Error(err)
		//	return nil, err
		//}
		//
		//if len(dealFiles) > 0 {
		//	dealFileId := dealFiles[0].ID
		//
		//	err := models.UpdateDealFileLockPaymentStatus(dealFileId, constants.LOCK_PAYMENT_STATUS_PROCESSING)
		//	if err != nil {
		//		logs.GetLogger().Error(err)
		//		return nil, err
		//	}
		//}
	}

	return event, nil
}