package logger

import (
	"blueberry/internal/config"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

var base zerolog.Logger

// splitLevelWriter 按级别分别写入 infoWriter 或 errWriter
type splitLevelWriter struct {
	infoWriter io.Writer
	errWriter  io.Writer
}

func (w splitLevelWriter) Write(p []byte) (n int, err error) {
	// 默认按 info 处理
	return w.infoWriter.Write(p)
}

func (w splitLevelWriter) WriteLevel(level zerolog.Level, p []byte) (n int, err error) {
	switch level {
	case zerolog.DebugLevel, zerolog.InfoLevel:
		return w.infoWriter.Write(p)
	default:
		return w.errWriter.Write(p)
	}
}

// Init 使用配置初始化全局日志（支持控制台 + 文件，带滚动）
func Init(cfg config.LoggingConfig) {
	zerolog.TimeFieldFormat = time.RFC3339

	// 级别
	if lvl, err := zerolog.ParseLevel(cfg.Level); err == nil {
		zerolog.SetGlobalLevel(lvl)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// 控制台漂亮输出（分别到 stdout/stderr）
	stdoutConsole := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339, NoColor: false}
	stderrConsole := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: false}

	// 文件（按配置与滚动）
	var outFile io.Writer
	var errFile io.Writer

	rot := cfg.Rotate
	newFileWriter := func(path string) io.Writer {
		if path == "" {
			return nil
		}
		maxSize := rot.MaxSizeMB
		if maxSize <= 0 {
			maxSize = 100
		}
		maxBackups := rot.MaxBackups
		if maxBackups <= 0 {
			maxBackups = 7
		}
		maxAge := rot.MaxAgeDays
		if maxAge <= 0 {
			maxAge = 30
		}
		return &lumberjack.Logger{
			Filename:   path,
			MaxSize:    maxSize,
			MaxBackups: maxBackups,
			MaxAge:     maxAge,
			Compress:   rot.Compress,
		}
	}

	// 支持单文件（file_path）或分别的 stdout_path / stderr_path
	if cfg.FilePath != "" {
		f := newFileWriter(cfg.FilePath)
		outFile = f
		errFile = f
	} else {
		outFile = newFileWriter(cfg.StdoutPath)
		errFile = newFileWriter(cfg.StderrPath)
	}

	// 组合 writer：控制台 + 文件
	infoWriters := []io.Writer{stdoutConsole}
	errWriters := []io.Writer{stderrConsole}
	if outFile != nil {
		infoWriters = append(infoWriters, outFile)
	}
	if errFile != nil {
		errWriters = append(errWriters, errFile)
	}

	lw := splitLevelWriter{
		infoWriter: io.MultiWriter(infoWriters...),
		errWriter:  io.MultiWriter(errWriters...),
	}

	base = zerolog.New(lw).With().Timestamp().Logger()
}

func SetLevel(level zerolog.Level) {
	zerolog.SetGlobalLevel(level)
}

func Info() *zerolog.Event  { return base.Info() }
func Error() *zerolog.Event { return base.Error() }
func Warn() *zerolog.Event  { return base.Warn() }
func Debug() *zerolog.Event { return base.Debug() }
func Fatal() *zerolog.Event { return base.Fatal() }
func Logger() zerolog.Logger {
	return base
}

func Printf(format string, v ...interface{}) {
	base.Info().Msgf(format, v...)
}
