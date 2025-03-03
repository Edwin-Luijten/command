package command

import (
	"errors"
	"runtime"
	"sync/atomic"
)

// Bus is the only struct exported and required for the command bus usage.
// The Bus should be instantiated using the NewBus function.
type Bus struct {
	workerPoolSize     int
	queueBuffer        int
	initialized        *uint32
	shuttingDown       *uint32
	workers            *uint32
	handlers           []Handler
	errorHandlers      []ErrorHandler
	asyncCommandsQueue chan Command
	closed             chan bool
}

// NewBus instantiates the Bus struct.
// The Initialization of the Bus is performed separately (Initialize function) for dependency injection purposes.
func NewBus() *Bus {
	return &Bus{
		workerPoolSize: runtime.GOMAXPROCS(0),
		queueBuffer:    100,
		initialized:    new(uint32),
		shuttingDown:   new(uint32),
		workers:        new(uint32),
		errorHandlers:  make([]ErrorHandler, 0),
		closed:         make(chan bool),
	}
}

// WorkerPoolSize may optionally be provided to tweak the worker pool size for async commands.
// It can only be adjusted *before* the bus is initialized.
// It defaults to the value returned by runtime.GOMAXPROCS(0).
func (bus *Bus) WorkerPoolSize(workerPoolSize int) {
	if !bus.isInitialized() {
		bus.workerPoolSize = workerPoolSize
	}
}

// QueueBuffer may optionally be provided to tweak the buffer size of the async commands queue.
// This value may have high impact on performance depending on the use case.
// It can only be adjusted *before* the bus is initialized.
// It defaults to 100.
func (bus *Bus) QueueBuffer(queueBuffer int) {
	if !bus.isInitialized() {
		bus.queueBuffer = queueBuffer
	}
}

// ErrorHandlers may optionally be provided.
// They will receive any error thrown during the command process.
func (bus *Bus) ErrorHandlers(hdls ...ErrorHandler) {
	if !bus.isInitialized() {
		bus.errorHandlers = hdls
	}
}

// Initialize the command bus.
func (bus *Bus) Initialize(hdls ...Handler) {
	if bus.initialize() {
		bus.handlers = hdls
		bus.asyncCommandsQueue = make(chan Command, bus.queueBuffer)
		for i := 0; i < bus.workerPoolSize; i++ {
			bus.workerUp()
			go bus.worker(bus.asyncCommandsQueue, bus.closed)
		}
		atomic.CompareAndSwapUint32(bus.shuttingDown, 1, 0)
	}
}

// HandleAsync the command using the workers asynchronously.
func (bus *Bus) HandleAsync(cmd Command) error {
	if err := bus.isValid(cmd); err != nil {
		return err
	}
	bus.asyncCommandsQueue <- cmd
	return nil
}

// Handle the command synchronously.
func (bus *Bus) Handle(cmd Command) error {
	if err := bus.isValid(cmd); err != nil {
		return err
	}
	return bus.handle(cmd)
}

// Shutdown the command bus gracefully.
// *Async commands handled while shutting down will be disregarded*.
func (bus *Bus) Shutdown() {
	if atomic.CompareAndSwapUint32(bus.shuttingDown, 0, 1) {
		bus.shutdown()
	}
}

//-----Private Functions------//

func (bus *Bus) initialize() bool {
	return atomic.CompareAndSwapUint32(bus.initialized, 0, 1)
}

func (bus *Bus) isInitialized() bool {
	return atomic.LoadUint32(bus.initialized) == 1
}

func (bus *Bus) isShuttingDown() bool {
	return atomic.LoadUint32(bus.shuttingDown) == 1
}

func (bus *Bus) worker(asyncCommandsQueue <-chan Command, closed chan<- bool) {
	for cmd := range asyncCommandsQueue {
		if cmd == nil {
			break
		}
		_ = bus.handle(cmd)
	}
	closed <- true
}

func (bus *Bus) handle(cmd Command) error {
	for _, hdl := range bus.handlers {
		if err := hdl.Handle(cmd); err != nil {
			bus.error(cmd, err)
			return err
		}
	}
	return nil
}

func (bus *Bus) workerUp() {
	atomic.AddUint32(bus.workers, 1)
}

func (bus *Bus) workerDown() {
	atomic.AddUint32(bus.workers, ^uint32(0))
}

func (bus *Bus) shutdown() {
	for atomic.LoadUint32(bus.workers) > 0 {
		bus.asyncCommandsQueue <- nil
		<-bus.closed
		bus.workerDown()
	}
	atomic.CompareAndSwapUint32(bus.initialized, 1, 0)
}

func (bus *Bus) isValid(cmd Command) error {
	var err error
	if cmd == nil {
		err = errors.New("invalid command")
		bus.error(cmd, err)
		return err
	}
	if !bus.isInitialized() {
		err = errors.New("the command bus is not initialized")
		bus.error(cmd, err)
		return err
	}
	if bus.isShuttingDown() {
		err = errors.New("the command bus is shutting down")
		bus.error(cmd, err)
		return err
	}
	return nil
}

func (bus *Bus) error(qry Command, err error) {
	for _, errHdl := range bus.errorHandlers {
		errHdl.Handle(qry, err)
	}
}
