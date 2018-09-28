package mempool

import (
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/thetatoken/ukulele/common"
	"github.com/thetatoken/ukulele/common/clist"
	"github.com/thetatoken/ukulele/common/math"
	"github.com/thetatoken/ukulele/core"
	dp "github.com/thetatoken/ukulele/dispatcher"
)

type mempoolTransaction struct {
	rawTransaction common.Bytes
}

//
// Mempool manages the transactions submitted by the clients
// or relayed from peers
//
type Mempool struct {
	mutex *sync.Mutex

	ledger     core.Ledger
	dispatcher *dp.Dispatcher

	txCandidates *clist.CList
	txBookeepper transactionBookkeeper
}

// CreateMempool creates an instance of Mempool
func CreateMempool(ledger core.Ledger, dispatcher *dp.Dispatcher) *Mempool {
	return &Mempool{
		mutex:        &sync.Mutex{},
		ledger:       ledger,
		dispatcher:   dispatcher,
		txCandidates: clist.New(),
		txBookeepper: createTransactionBookkeeper(defaultMaxNumTxs),
	}
}

// InsertTransaction inserts the incoming transaction to mempool (submitted by the clients or relayed from peers)
func (mp *Mempool) InsertTransaction(mptx *mempoolTransaction) error {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	if mp.txBookeepper.hasSeen(mptx) {
		log.Infof("Transaction already seen: %v", mptx)
		return nil
	}

	mp.txBookeepper.record(mptx)

	txBytes := mptx.rawTransaction
	checkTxRes := mp.ledger.CheckTx(txBytes)
	if !checkTxRes.IsOK() {
		return errors.New(checkTxRes.Message)
	}

	mp.txCandidates.PushBack(mptx)

	return nil
}

// Start needs to be called when the Mempool starts
func (mp *Mempool) Start() error {
	go mp.broadcastTransactionsRoutine()
	return nil
}

// Stop needs to be called when the Mempool stops
func (mp *Mempool) Stop() {
}

// Lock is for the caller to lock/unlock the Mempool and perform safely update
func (mp *Mempool) Lock() {
	mp.mutex.Lock()
}

// Unlock is for the caller to lock/unlock the Mempool and perform safely update
func (mp *Mempool) Unlock() {
	mp.mutex.Unlock()
}

// Size returns the number of transactions in the Mempool
func (mp *Mempool) Size() int {
	return mp.txCandidates.Len()
}

// Reap returns a list of valid raw transactions. maxNumTxs == 0 means
// none, maxNumTxs < 0 means uncapped. Note that Reap does NOT remove
// the transactions from the txCandidates list. Instead, the consensus
// engine needs to call the Mempool.Update() function to remove the
// committed transactions
func (mp *Mempool) Reap(maxNumTxs int) []common.Bytes {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	if maxNumTxs == 0 {
		return []common.Bytes{}
	} else if maxNumTxs < 0 {
		maxNumTxs = mp.txCandidates.Len()
	} else {
		maxNumTxs = math.MinInt(mp.txCandidates.Len(), maxNumTxs)
	}

	txs := make([]common.Bytes, 0, maxNumTxs)
	for e := mp.txCandidates.Front(); e != nil && len(txs) < maxNumTxs; e = e.Next() {
		mptx := e.Value.(*mempoolTransaction)
		txs = append(txs, mptx.rawTransaction)
	}

	return txs
}

// Update removes the committed transactions from the transaction candidate list
func (mp *Mempool) Update(committedRawTxs []common.Bytes) bool {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	committedRawTxMap := make(map[string]bool)
	for _, rawtx := range committedRawTxs {
		committedRawTxMap[string(rawtx)] = true
	}

	for e := mp.txCandidates.Front(); e != nil; e = e.Next() {
		rawmptx := e.Value.(*mempoolTransaction).rawTransaction
		if _, exists := committedRawTxMap[string(rawmptx[:])]; exists {
			mp.txCandidates.Remove(e)
			e.DetachPrev()
		}
	}

	return true
}

// Flush removes all transactions from the Mempool and the transactionBookkeeper
func (mp *Mempool) Flush() {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()

	mp.txBookeepper.reset()

	for e := mp.txCandidates.Front(); e != nil; e = e.Next() {
		mp.txCandidates.Remove(e)
		e.DetachPrev()
	}
}

// broadcastTransactionRoutine broadcasts transactions to neighoring peers
func (mp *Mempool) broadcastTransactionsRoutine() {
	var next *clist.CElement
	for {
		if next == nil {
			next = mp.txCandidates.FrontWait() // Wait until a tx is available
		}

		mptx := next.Value.(*mempoolTransaction)

		// Broadcast the transaction
		data := dp.DataResponse{
			ChannelID: common.ChannelIDTransaction,
			Checksum:  []byte(""), // TODO: calculate the checksum
			Payload:   mptx.rawTransaction,
		}

		log.Debugf(">>>>> broadcasting mptx: %v", string(mptx.rawTransaction[:]))
		peerIDs := []string{} // empty peerID list means broadcasting to all neighboring peers
		mp.dispatcher.SendData(peerIDs, data)

		next = next.NextWait()
	}
}