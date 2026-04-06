package logger

import (
	"fmt"
	"log"
	"os"
	"time"
)

type Logger struct {
	infoLogger      *log.Logger
	debugLogger     *log.Logger
	warnLogger      *log.Logger
	errorLogger     *log.Logger
	testLogger      *log.Logger
	timestampFormat string
}

const (
	LOGOFF = false
)

// Init 初始化日志系统，为每个节点创建日志文件
func NewLogger(nodeID int, role string) *Logger {
	// 创建logs目录
	os.MkdirAll("logs", 0755)
	logFile := ""
	testLogFile := ""

	// 生成日志文件名
	switch role {
	case "node":
		logFile = fmt.Sprintf("logs/node_%d.log", nodeID)
		testLogFile = fmt.Sprintf("logs/node_%d_test.log", nodeID)
	case "client":
		logFile = "logs/client.log"
		testLogFile = "logs/client_test.log"
	case "blockchain":
		logFile = "logs/blockchain.log"
	case "result":
		logFile = "logs/result.log"
	default:
		logFile = "logs/others.log"
	}

	// 打开日志文件
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	testFile := &os.File{}

	if role == "client" || role == "node" {
		testFile, err = os.OpenFile(testLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			log.Fatal("Failed to open test log file:", err)
		}
	}

	// 创建自定义时间戳格式，包含微秒精度
	timestampFormat := "2006-01-02 15:04:05.000000"

	l := &Logger{
		infoLogger:  log.New(file, "[INFO] ", 0),
		debugLogger: log.New(file, "[DEBUG] ", 0),
		warnLogger:  log.New(file, "[WARN] ", 0),
		errorLogger: log.New(file, "[ERROR] ", 0),
		testLogger:  log.New(testFile, "[TEST] ", 0),
	}

	// 设置自定义时间戳格式
	l.setTimestampFormat(timestampFormat)
	return l
}

// setTimestampFormat 设置时间戳格式
func (l *Logger) setTimestampFormat(format string) {
	l.timestampFormat = format
}

// formatLogMessage 格式化日志消息，添加高精度时间戳
func (l *Logger) formatLogMessage(level, format string, args ...interface{}) string {
	timestamp := time.Now().Format(l.timestampFormat)
	message := fmt.Sprintf(format, args...)
	return fmt.Sprintf("%s %s %s", timestamp, level, message)
}

// Info 记录信息日志
func (l *Logger) Info(format string, args ...interface{}) {
	if l.infoLogger != nil {
		message := l.formatLogMessage("[INFO]", format, args...)
		l.infoLogger.Print(message)
	}
}

// Debug 记录调试日志
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.debugLogger != nil {
		message := l.formatLogMessage("[DEBUG]", format, args...)
		l.debugLogger.Print(message)
	}
}

// Warn 记录警告日志
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.warnLogger != nil {
		message := l.formatLogMessage("[WARN]", format, args...)
		l.warnLogger.Print(message)
	}
}

// Error 记录错误日志
func (l *Logger) Error(format string, args ...interface{}) {
	if l.errorLogger != nil {
		message := l.formatLogMessage("[ERROR]", format, args...)
		l.errorLogger.Print(message)
	}
}

// Test 记录测试日志
func (l *Logger) Test(format string, args ...interface{}) {
	if LOGOFF {
		return
	}
	if l.testLogger != nil {
		message := l.formatLogMessage("[TEST]", format, args...)
		l.testLogger.Print(message)
	}
}
