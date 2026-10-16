package pfcp

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/wmnsk/go-pfcp/message"

	"github.com/free5gc/smf/internal/logger"
	"github.com/free5gc/smf/pkg/factory"
)

const (
	PfcpPort      = 8805
	MaxPfcpMsgLen = 65536
	rcvChLen      = 8192
	txDispChLen   = rcvChLen / 2

	udpReadRetryInitialDelay = 5 * time.Millisecond
	udpReadRetryMaximumDelay = time.Second
)

type TransType string

const (
	TX TransType = "TX"
	RX TransType = "RX"
)

type smfIface interface {
	Config() *factory.Config
}

type pfcpConn interface {
	Close() error
	LocalAddr() net.Addr
	ReadFromUDP([]byte) (int, *net.UDPAddr, error)
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
}

// PfcpServer owns SMF's PFCP UDP socket and transaction state. PFCP wire
// encoding remains the responsibility of github.com/wmnsk/go-pfcp.
type PfcpServer struct {
	smfIface

	addr string
	port int
	conn pfcpConn

	rcvCh  chan RcvPfcpMsg
	txCh   chan TransmitMessage
	dispCh chan RcvPfcpMsg
	stopCh chan struct{}

	stopOnce sync.Once
	txTrans  sync.Map // map[TransactionID]*TxTransaction
	rxTrans  sync.Map // map[TransactionID]*RxTransaction
	seqAlloc *SeqAllocator

	retransTimeout time.Duration
	maxRetrans     uint8
	recoveryTime   time.Time

	dispatchMu      sync.RWMutex
	dispatch        func(message.Message, *net.UDPAddr)
	dispatchWorkers int

	associationMu    sync.RWMutex
	associationState AssociationStateManager

	sessionReportMu      sync.RWMutex
	sessionReportHandler SessionReportHandler
	log                  *logrus.Entry
}

func NewPfcpServer(smf smfIface, addr string) *PfcpServer {
	retransTimeout := factory.PfcpDefaultRetransTimeout
	maxRetrans := factory.PfcpDefaultMaxRetrans
	dispatchWorkers := factory.PfcpDefaultDispatchWorkers
	if smf != nil && smf.Config() != nil {
		retransTimeout, maxRetrans = smf.Config().GetPfcpRetransTimer()
		dispatchWorkers = smf.Config().GetPfcpDispatchWorkerCount()
	}

	server := &PfcpServer{
		smfIface:        smf,
		addr:            addr,
		port:            PfcpPort,
		rcvCh:           make(chan RcvPfcpMsg, rcvChLen),
		txCh:            make(chan TransmitMessage, txDispChLen),
		dispCh:          make(chan RcvPfcpMsg, txDispChLen),
		stopCh:          make(chan struct{}),
		seqAlloc:        NewSeqAllocator(),
		retransTimeout:  retransTimeout,
		maxRetrans:      maxRetrans,
		recoveryTime:    time.Now(),
		dispatchWorkers: dispatchWorkers,
		log: logger.PfcpLog.WithField(
			"addr", fmt.Sprintf("%s:%d", addr, PfcpPort),
		),
	}
	server.SetDispatch(server.Dispatch)
	return server
}

func (s *PfcpServer) RecoveryTime() time.Time {
	return s.recoveryTime
}

func (s *PfcpServer) Listen() error {
	var ip net.IP
	if s.addr == "" {
		ip = net.IPv4zero
	} else {
		ip = net.ParseIP(s.addr)
		if ip == nil {
			return fmt.Errorf("invalid PFCP listen address %q", s.addr)
		}
	}

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: s.port})
	if err != nil {
		return err
	}
	s.conn = conn
	s.log = logger.PfcpLog.WithField("addr", conn.LocalAddr())
	return nil
}

func (s *PfcpServer) LocalAddr() *net.UDPAddr {
	if s == nil || s.conn == nil {
		return nil
	}
	addr, _ := s.conn.LocalAddr().(*net.UDPAddr)
	return addr
}

func (s *PfcpServer) Run(wg *sync.WaitGroup) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("PFCP server startup panic: %v", recovered)
			logger.PfcpLog.Errorf("%v\n%s", err, debug.Stack())
		}
	}()

	if err = s.Listen(); err != nil {
		return fmt.Errorf("PFCP failed to listen: %w", err)
	}

	s.log.Infof("Listen on %v", s.conn.LocalAddr())
	wg.Add(2 + s.dispatchWorkers)
	go s.main(wg)
	go s.receiver(wg)
	for worker := 0; worker < s.dispatchWorkers; worker++ {
		go s.dispatcher(wg)
	}
	s.log.Infoln("PFCP server started")
	return nil
}

func (s *PfcpServer) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		if s.conn != nil {
			if err := s.conn.Close(); err != nil && !isServerStopped(s.stopCh) {
				s.log.Errorf("stop PFCP server: %v", err)
			}
		}
	})
}

func (s *PfcpServer) main(wg *sync.WaitGroup) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Fatalf("panic in PFCP main loop: %v\n%s", recovered, debug.Stack())
		}
		s.stopTransactions()
		s.log.Infoln("PFCP main loop stopped")
		wg.Done()
	}()

	for {
		select {
		case <-s.stopCh:
			return
		case txMsg := <-s.txCh:
			var sendErr error
			if txMsg.TrType == TX {
				sendErr = s.sendReqTo(txMsg.Context, txMsg.Msg, txMsg.RemoteAddr, txMsg.RspCh)
			} else {
				sendErr = s.sendRspTo(txMsg.Msg, txMsg.RemoteAddr)
			}
			if sendErr != nil {
				s.log.Errorf("send PFCP message: %v", sendErr)
			}
			if txMsg.Done != nil {
				txMsg.Done <- sendErr
				close(txMsg.Done)
			}
		case rcvMsg := <-s.rcvCh:
			if rcvMsg.Msg == nil {
				continue
			}
			s.transactionHandler(rcvMsg.Msg, &rcvMsg.RemoteAddr)
		}
	}
}

func (s *PfcpServer) transactionHandler(msg message.Message, addr *net.UDPAddr) {
	transactionID := TransactionID(addr, msg.Sequence())
	if msg.IsRequest() {
		rx, found := s.loadRxTransaction(transactionID)
		if !found {
			rx = s.newRxTransaction(addr, msg.Sequence())
		}
		needDispatch, err := rx.recv(found)
		if err != nil {
			s.log.Warnf("receive PFCP request: %v", err)
			return
		}
		if !needDispatch {
			return
		}

		select {
		case s.dispCh <- RcvPfcpMsg{RemoteAddr: *addr, Msg: msg, Rx: rx}:
		default:
			// Do not retain a response transaction for work that was not
			// admitted. A retransmission can then be admitted after pressure
			// subsides instead of being mistaken for an already queued request.
			rx.stop()
			s.log.Warnf("PFCP dispatch queue full; dropping %s sequence %#x from %v",
				msg.MessageTypeName(), msg.Sequence(), addr)
		}
		return
	}

	tx, found := s.loadTxTransaction(transactionID)
	if !found {
		s.log.Debugf("no PFCP request transaction %q found for response", transactionID)
		return
	}
	tx.complete(RcvPfcpMsg{RemoteAddr: *addr, Msg: msg})
}

func (s *PfcpServer) receiver(wg *sync.WaitGroup) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Fatalf("panic in PFCP receiver: %v\n%s", recovered, debug.Stack())
		}
		s.log.Infoln("PFCP receiver stopped")
		wg.Done()
	}()

	buffer := make([]byte, MaxPfcpMsgLen)
	retryDelay := time.Duration(0)
	for {
		n, addr, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			if isServerStopped(s.stopCh) {
				return
			}
			if errors.Is(err, net.ErrClosed) {
				s.log.Errorf("PFCP UDP socket closed unexpectedly: %v", err)
				s.Stop()
				return
			}

			s.log.Warnf("PFCP ReadFromUDP: %v; retrying in %s", err, retryDelay)
			if !waitForUDPReadRetry(s.stopCh, retryDelay) {
				return
			}
			retryDelay = nextUDPReadRetryDelay(retryDelay)
			continue
		}
		retryDelay = 0

		packet := make([]byte, n)
		copy(packet, buffer[:n])
		if err = validatePfcpPacketLength(packet); err != nil {
			s.log.Warnf("invalid PFCP packet from %v: %v", addr, err)
			continue
		}
		s.log.Tracef("received PFCP message (len=%d):\n%s", n, hex.Dump(packet))

		msg, parseErr := message.Parse(packet)
		if parseErr != nil {
			s.log.Warnf("parse PFCP message from %v: %v", addr, parseErr)
			continue
		}
		if _, unknown := msg.(*message.Generic); unknown {
			s.log.Warnf("received unknown PFCP message type %d from %v", msg.MessageType(), addr)
			continue
		}
		sendToRcvCh(s.stopCh, s.rcvCh, RcvPfcpMsg{RemoteAddr: *addr, Msg: msg}, s.log)
	}
}

func nextUDPReadRetryDelay(previous time.Duration) time.Duration {
	if previous == 0 {
		return udpReadRetryInitialDelay
	}
	next := previous * 2
	if next > udpReadRetryMaximumDelay {
		return udpReadRetryMaximumDelay
	}
	return next
}

func waitForUDPReadRetry(stop <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

func validatePfcpPacketLength(packet []byte) error {
	if len(packet) < 4 {
		return fmt.Errorf("packet is shorter than the PFCP header: %d", len(packet))
	}
	want := int(binary.BigEndian.Uint16(packet[2:4])) + 4
	if len(packet) != want {
		return fmt.Errorf("packet length is %d, header declares %d", len(packet), want)
	}
	return nil
}

func sendToRcvCh(
	stop <-chan struct{},
	destination chan<- RcvPfcpMsg,
	msg RcvPfcpMsg,
	log *logrus.Entry,
) {
	select {
	case <-stop:
		return
	default:
	}

	select {
	case destination <- msg:
	default:
		log.Warnf("PFCP receive queue full; dropping %s sequence %#x from %v",
			msg.Msg.MessageTypeName(), msg.Msg.Sequence(), msg.RemoteAddr)
	}
}

// sendRequest sends one concrete go-pfcp request through the server-owned
// transaction layer. Every caller supplies a context, so queueing, response
// waiting, retransmission, and sequence ownership share one cancellation path.
func (s *PfcpServer) sendRequest(
	ctx context.Context,
	request message.Message,
	addr *net.UDPAddr,
) (message.Message, error) {
	if s == nil || request == nil || !request.IsRequest() || addr == nil {
		return nil, fmt.Errorf("send PFCP request: invalid request")
	}
	if ctx == nil {
		return nil, fmt.Errorf("send PFCP request: nil context")
	}
	responseChannel := make(chan RcvPfcpMsg, 1)
	transmit := TransmitMessage{
		Context: ctx, Msg: request, RemoteAddr: addr, TrType: TX, RspCh: responseChannel,
	}
	select {
	case <-s.stopCh:
		return nil, fmt.Errorf("send PFCP request to %v: PFCP server is stopped", addr)
	case <-ctx.Done():
		return nil, fmt.Errorf("send PFCP request to %v: %w", addr, ctx.Err())
	case s.txCh <- transmit:
	}

	select {
	case <-s.stopCh:
		return nil, fmt.Errorf("send PFCP request to %v: PFCP server is stopped", addr)
	case <-ctx.Done():
		return nil, fmt.Errorf("send PFCP request to %v: %w", addr, ctx.Err())
	case received, ok := <-responseChannel:
		if !ok || received.Msg == nil {
			if isServerStopped(s.stopCh) {
				return nil, fmt.Errorf("send PFCP request to %v: PFCP server is stopped", addr)
			}
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("send PFCP request to %v: %w", addr, err)
			}
			return nil, fmt.Errorf("PFCP request to %v timed out", addr)
		}
		return received.Msg, nil
	}
}

// SendPfcpResponse queues a response through the server main loop and waits
// until it has been written (or failed). Association side effects must run
// only after this local write succeeds.
func (s *PfcpServer) SendPfcpResponse(msg message.Message, addr *net.UDPAddr) error {
	if s == nil || msg == nil || addr == nil {
		return fmt.Errorf("send PFCP response: invalid argument")
	}
	if msg.IsRequest() {
		return fmt.Errorf("message type %d is not a PFCP response", msg.MessageType())
	}

	done := make(chan error, 1)
	transmit := TransmitMessage{Msg: msg, RemoteAddr: addr, TrType: RX, Done: done}
	select {
	case <-s.stopCh:
		return fmt.Errorf("send PFCP response to %v: PFCP server is stopped", addr)
	case s.txCh <- transmit:
	}

	select {
	case <-s.stopCh:
		return fmt.Errorf("send PFCP response to %v: PFCP server is stopped", addr)
	case err := <-done:
		return err
	}
}

func (s *PfcpServer) sendReqTo(
	ctx context.Context,
	msg message.Message,
	addr *net.UDPAddr,
	response chan RcvPfcpMsg,
) error {
	if !msg.IsRequest() {
		return fmt.Errorf("message type %d is not a PFCP request", msg.MessageType())
	}
	tx, err := s.newTxTransaction(addr, response)
	if err != nil {
		return err
	}
	tx.watchContext(ctx)
	if err = tx.send(msg); err != nil {
		tx.complete(RcvPfcpMsg{Msg: nil})
		return err
	}
	return nil
}

func (s *PfcpServer) sendRspTo(msg message.Message, addr *net.UDPAddr) error {
	if msg.IsRequest() {
		return fmt.Errorf("message type %d is not a PFCP response", msg.MessageType())
	}
	rx, found := s.loadRxTransaction(TransactionID(addr, msg.Sequence()))
	if !found {
		return fmt.Errorf("PFCP response transaction for %v sequence %#x was not found", addr, msg.Sequence())
	}
	if err := rx.send(msg); err != nil {
		// A failed local write must not leave a cached response behind. The UPF
		// can retransmit the request and receive a freshly handled response.
		rx.stop()
		return err
	}
	return nil
}

func (s *PfcpServer) loadTxTransaction(id string) (*TxTransaction, bool) {
	value, found := s.txTrans.Load(id)
	if !found {
		return nil, false
	}
	tx, ok := value.(*TxTransaction)
	return tx, ok
}

func (s *PfcpServer) loadRxTransaction(id string) (*RxTransaction, bool) {
	value, found := s.rxTrans.Load(id)
	if !found {
		return nil, false
	}
	rx, ok := value.(*RxTransaction)
	return rx, ok
}

func (s *PfcpServer) stopTransactions() {
	s.txTrans.Range(func(_, value any) bool {
		if tx, ok := value.(*TxTransaction); ok {
			tx.complete(RcvPfcpMsg{Msg: nil})
		}
		return true
	})
	s.rxTrans.Range(func(_, value any) bool {
		if rx, ok := value.(*RxTransaction); ok {
			rx.stop()
		}
		return true
	})
}

func isServerStopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
