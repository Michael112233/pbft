package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

type Logger struct {
	infoLogger  *log.Logger
	debugLogger *log.Logger
	warnLogger  *log.Logger
	errorLogger *log.Logger
}

// Init 初始化日志系统，为每个节点创建日志文件
func NewLogger(nodeID int64, role string) *Logger {
	// 创建logs目录
	os.MkdirAll("logs", 0755)
	logFile := ""

	// 生成日志文件名
	if role == "node" {
		timestamp := time.Now().Format("2006-01-02_15-04-05")
		logFile = fmt.Sprintf("logs/node_%d_%s.log", nodeID, timestamp)
	} else if role == "client" {
		timestamp := time.Now().Format("2006-01-02_15-04-05")
		logFile = fmt.Sprintf("logs/client_%s.log", timestamp)
	} else {
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
