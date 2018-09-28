package execution

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/thetatoken/ukulele/common"
	"github.com/thetatoken/ukulele/common/result"
	"github.com/thetatoken/ukulele/ledger/types"
)

var _ TxExecutor = (*ReleaseFundTxExecutor)(nil)

// ------------------------------- ReleaseFundTx Transaction -----------------------------------

// ReleaseFundTxExecutor implements the TxExecutor interface
type ReleaseFundTxExecutor struct {
}

// NewReleaseFundTxExecutor creates a new instance of ReleaseFundTxExecutor
func NewReleaseFundTxExecutor() *ReleaseFundTxExecutor {
	return &ReleaseFundTxExecutor{}
}

func (exec *ReleaseFundTxExecutor) sanityCheck(chainID string, view types.ViewDataGetter, transaction types.Tx) result.Result {
	tx := transaction.(*types.ReleaseFundTx)

	// Validate source, basic
	res := tx.Source.ValidateBasic()
	if res.IsError() {
		return res
	}

	// Get input account
	sourceAccount, success := getInput(view, tx.Source)
	if success.IsError() {
		return result.Error("Unknown address: %v", tx.Source.Address)
	}

	// Validate input, advanced
	signBytes := tx.SignBytes(chainID)
	res = validateInputAdvanced(sourceAccount, signBytes, tx.Source)
	if res.IsError() {
		log.Infof(fmt.Sprintf("validateSourceAdvanced failed on %X: %v", tx.Source.Address, res))
		return res
	}

	if !sanityCheckForFee(tx.Fee) {
		return result.Error("invalid fee")
	}

	minimalBalance := types.Coins{tx.Fee}
	if !sourceAccount.Balance.IsGTE(minimalBalance) {
		log.Infof(fmt.Sprintf("Source did not have enough balance %X", tx.Source.Address))
		return result.Error("Source balance is %v, but required minimal balance is %v", sourceAccount.Balance, minimalBalance)
	}

	currentBlockHeight := GetCurrentBlockHeight()
	reserveSequence := tx.ReserveSequence
	err := sourceAccount.CheckReleaseFund(currentBlockHeight, reserveSequence)
	if err != nil {
		return result.Error(err.Error())
	}

	return result.OK
}

func (exec *ReleaseFundTxExecutor) process(chainID string, view types.ViewDataAccessor, transaction types.Tx) (common.Hash, result.Result) {
	tx := transaction.(*types.ReleaseFundTx)

	sourceInputs := []types.TxInput{tx.Source}
	accounts, success := getInputs(view, sourceInputs)
	if success.IsError() {
		// TODO: revisit whether we should panic or just log the error.
		return common.Hash{}, result.Error("Failed to get the source account")
	}
	sourceAddress := tx.Source.Address
	sourceAccount := accounts[string(sourceAddress[:])]

	reserveSequence := tx.ReserveSequence

	currentBlockHeight := GetCurrentBlockHeight()
	sourceAccount.ReleaseFund(currentBlockHeight, reserveSequence)
	if !chargeFee(sourceAccount, tx.Fee) {
		return common.Hash{}, result.Error("failed to charge transaction fee")
	}

	sourceAccount.Sequence++
	view.SetAccount(sourceAddress, sourceAccount)

	txHash := types.TxID(chainID, tx)
	return txHash, result.OK
}