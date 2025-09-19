package logger

import (
	"fmt"
	"log"
	"os"
)

type Logger struct {
	infoLogger  *log.Logger
	debugLogger *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger
	testLogger  *log.Logger
}

// Init 初始化日志系统，为每个节点创建日志文件
func NewLogger(nodeID int64, role string) *Logger {
	// 创建logs目录
	os.MkdirAll("logs", 0755)
	logFile := ""

	// 生成日志文件名
	switch role {
	case "node":
		logFile = fmt.Sprintf("logs/node_%d.log", nodeID)
	case "client":
		logFile = "logs/client.log"
	case "blockchain":
		logFile = "logs/blockchain.log"
	default:
		logFile = "logs/others.log"
	}

	// 打开日志文件
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}

	l := &Logger{
		infoLogger:  log.New(file, "[INFO] ", log.LstdFlags),
		debugLogger: log.New(file, "[DEBUG] ", log.LstdFlags),
		warnLogger:  log.New(file, "[WARN] ", log.LstdFlags),
		errorLogger: log.New(file, "[ERROR] ", log.LstdFlags),
		testLogger:  log.New(file, "[TEST] ", log.LstdFlags),
	}
	return l
}

// Info 记录信息日志
func (l *Logger) Info(format string, args ...interface{}) {
	if l.infoLogger != nil {
		l.infoLogger.Printf(format, args...)
	}
}

// Debug 记录调试日志
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.debugLogger != nil {
		l.debugLogger.Printf(format, args...)
	}
}

// Warn 记录警告日志
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.warnLogger != nil {
		l.warnLogger.Printf(format, args...)
	}
}

// Error 记录错误日志
func (l *Logger) Error(format string, args ...interface{}) {
	if l.errorLogger != nil {
		l.errorLogger.Printf(format, args...)
	}
}

// Test 记录测试日志
func (l *Logger) Test(format string, args ...interface{}) {
	if l.testLogger != nil {
		l.testLogger.Printf(format, args...)
	}
}
