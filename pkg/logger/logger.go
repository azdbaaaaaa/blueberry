package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

var log zerolog.Logger

func init() {
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
		NoColor:    false,
	}

	log = zerolog.New(output).With().
		Timestamp().
		Logger()
}

func SetLevel(level zerolog.Level) {
	zerolog.SetGlobalLevel(level)
}

func Info() *zerolog.Event {
	return log.Info()
}

func Error() *zerolog.Event {
	return log.Error()
}

func Warn() *zerolog.Event {
	return log.Warn()
}

func Debug() *zerolog.Event {
	return log.Debug()
}

func Fatal() *zerolog.Event {
	return log.Fatal()
}

func Logger() zerolog.Logger {
	return log
}

func Printf(format string, v ...interface{}) {
	log.Info().Msgf(format, v...)
}

