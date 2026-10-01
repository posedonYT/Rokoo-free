package kafkax

import (
	"context"
	"strconv"
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

var retryBackoffs = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}

type dlqWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

func DLQTopic(topic string) string { return topic + ".dlq" }

func handleWithRetry(ctx context.Context, h Handler, msg kafka.Message, backoffs []time.Duration, dlq dlqWriter, sleep func(context.Context, time.Duration) error) error {
	err := h(ctx, msg)
	for i := 0; err != nil && i < len(backoffs); i++ {
		if serr := sleep(ctx, backoffs[i]); serr != nil {
			return serr
		}
		err = h(ctx, msg)
	}
	if err == nil {
		return nil
	}
	headers := append(append([]kafka.Header{}, msg.Headers...),
		kafka.Header{Key: "dlq_error", Value: []byte(err.Error())},
		kafka.Header{Key: "dlq_source_partition", Value: []byte(strconv.Itoa(msg.Partition))},
		kafka.Header{Key: "dlq_source_offset", Value: []byte(strconv.FormatInt(msg.Offset, 10))},
	)
	return dlq.WriteMessages(ctx, kafka.Message{
		Topic:   DLQTopic(msg.Topic),
		Key:     msg.Key,
		Value:   msg.Value,
		Headers: headers,
	})
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func Consume(ctx context.Context, brokers []string, group string, topics []string, h Handler) error {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        brokers,
		GroupID:        group,
		GroupTopics:    topics,
		MinBytes:       1,
		MaxBytes:       10e6,
		CommitInterval: time.Second,
		StartOffset:    kafka.FirstOffset,
	})
	defer r.Close()
	dlq := NewWriter(brokers)
	defer dlq.Close()
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			return err
		}
		if err := handleWithRetry(ctx, h, msg, retryBackoffs, dlq, sleepCtx); err != nil {
			return err
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			return err
		}
	}
}
