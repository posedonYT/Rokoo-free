package kafkax

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
)

func NewWriter(brokers []string) *kafka.Writer {
	return &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Balancer:               &kafka.Hash{},
		RequiredAcks:           kafka.RequireAll,
		BatchTimeout:           20 * time.Millisecond,
		AllowAutoTopicCreation: true,
	}
}

func Publish(ctx context.Context, w *kafka.Writer, topic, key string, value []byte) error {
	return w.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: value,
	})
}

type Handler func(ctx context.Context, msg kafka.Message) error

func Consume(ctx context.Context, brokers []string, group string, topics []string, h Handler) error {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		GroupID:        group,
		GroupTopics:    topics,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
	})
	defer r.Close()
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			return err
		}
		if err := h(ctx, msg); err != nil {
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			return err
		}
	}
}
