package config

import (
	nested "github.com/antonfisher/nested-logrus-formatter"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	ApplicationNamespace string `mapstructure:"APP_APPLICATION_NAMESPACE"`
	LogLevel             string `mapstructure:"APP_LOG_LEVEL"`
	LogFormat            string `mapstructure:"APP_LOG_FORMAT"`
}

const (
	applicationNamespaceKey = "APP_APPLICATION_NAMESPACE"
	logLevelKey             = "APP_LOG_LEVEL"
	logFormatKey            = "APP_LOG_FORMAT"
)

var (
	EnvConfig = loadConfig()
)

func loadConfig() *Config {
	viper.AutomaticEnv()

	var conf Config

	viper.SetDefault(applicationNamespaceKey, "argocd")
	viper.SetDefault(logLevelKey, "info")
	viper.SetDefault(logFormatKey, "nested")

	conf.ApplicationNamespace = viper.GetString(applicationNamespaceKey)
	conf.LogLevel = viper.GetString(logLevelKey)
	conf.LogFormat = viper.GetString(logFormatKey)

	conf.initLogger()

	return &conf
}

func (c *Config) initLogger() {
	const defaultLogLevel = "info"
	const defaultLogFormat = "nested"

	logLevelValue, err := logrus.ParseLevel(c.LogLevel)
	if err != nil {
		logrus.Warnf(
			"В параметре '%v' указан некорректный уровень отладки [%v], будет использован умолчательный [%v]. "+
				"Возможные значения: trace, debug, info, warn, warning, error, fatal, panic", logLevelKey, c.LogLevel, defaultLogLevel,
		)

		logLevelValue = logrus.InfoLevel
	}
	logrus.SetLevel(logLevelValue)

	logFormatValue := c.LogFormat
	if logFormatValue != "json" && logFormatValue != defaultLogFormat {
		logrus.Warnf("В параметре '%v' указан некорректный формат отладки [%v], будет использован умолчательный [%v]. "+
			"Возможные значения: json, nested", logFormatKey, logFormatValue, defaultLogFormat,
		)

		logFormatValue = defaultLogFormat
	}

	if logFormatValue == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyTime: "@timestamp",
				logrus.FieldKeyMsg:  "message",
			},
		})
	} else if logFormatValue == defaultLogFormat {
		logrus.SetFormatter(&nested.Formatter{
			HideKeys:      true,
			ShowFullLevel: true,
		})
	}
}
