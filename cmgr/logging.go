package cmgr

import (
	"fmt"
	"log"
)

type LogLevel int

const (
	DISABLED LogLevel = iota
	ERROR
	WARN
	INFO
	DEBUG
)

type logger struct {
	logger   *log.Logger
	logLevel LogLevel
}

// Wrapper around the
func newLogger(logLevel LogLevel) *logger {
	l := new(logger)
	l.logger = log.New(log.Writer(), "cmgr: ", log.Flags())
	l.logLevel = logLevel
	return l
}

func (l *logger) debug(v ...interface{}) {
	if l.logLevel >= DEBUG {
		l.logger.Print(append([]interface{}{"DEBUG: "}, v...))
	}
}

func (l *logger) debugf(format string, v ...interface{}) {
	l.debug(fmt.Sprintf(format, v...))
}

func (l *logger) info(v ...interface{}) {
	if l.logLevel >= INFO {
		l.logger.Print(append([]interface{}{"INFO: "}, v...))
	}
}

func (l *logger) infof(format string, v ...interface{}) {
	l.info(fmt.Sprintf(format, v...))
}

func (l *logger) warn(v ...interface{}) {
	if l.logLevel >= WARN {
		l.logger.Print(append([]interface{}{"WARN: "}, v...))
	}
}

func (l *logger) warnf(format string, v ...interface{}) {
	l.warn(fmt.Sprintf(format, v...))
}

func (l *logger) error(v ...interface{}) {
	if l.logLevel >= ERROR {
		l.logger.Print(append([]interface{}{"ERROR: "}, v...))
	}
}

func (l *logger) errorf(format string, v ...interface{}) {
	l.error(fmt.Sprintf(format, v...))
}
