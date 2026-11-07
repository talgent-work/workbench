package integrationmessage

type Config struct {
	ConsumerGroup string `json:"consumer_group" toml:"consumer_group" yaml:"consumer_group"`
}
