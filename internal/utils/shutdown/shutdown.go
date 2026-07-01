package shutdown

import (
	"os"
	"os/signal"
	"syscall"
)

type logger interface {
	Infof(template string, args ...interface{})
	Errorf(template string, args ...interface{})
	Warnf(template string, args ...interface{})
	Debugf(template string, args ...interface{})
}

var ilog logger
var funcs []func() error

func Init(log logger) {
	ilog = log
	funcs = make([]func() error, 0)
}

func Register(fn func() error) {
	funcs = append(funcs, fn)
}

func Listen() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	ilog.Infof("Program started, press Ctrl+C to exit")
	sig := <-quit
	ilog.Warnf("Received exit signal: %v", sig)
	if len(funcs) == 0 {
		return
	}
	for i := len(funcs) - 1; i >= 0; i-- {
		if err := funcs[i](); err != nil {
			ilog.Errorf("Closing functions execution failed: %v", err)
		}
	}
	ilog.Infof("Shutdown completed successfully")
	os.Exit(0)
}

func Shutdown() {
	for i := len(funcs) - 1; i >= 0; i-- {
		if err := funcs[i](); err != nil {
			ilog.Errorf("Closing functions execution failed: %v", err)
		}
	}
	ilog.Infof("Shutdown completed successfully")
}
