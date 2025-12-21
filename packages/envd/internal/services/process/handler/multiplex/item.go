package multiplex

type AckFunc func(success bool)

type Item[T any] struct {
	value   T
	success chan bool
}

func newItem[T any](v T) *Item[T] {
	return &Item[T]{
		value:   v,
		success: make(chan bool, 1),
	}
}

func (i *Item[T]) Value() T {
	v, ack := i.ValueWithAck()

	ack(true)

	return v
}

// ValueWithAck returns the value and a function to ack the item.
// If the acknowledge is false, the item can be replayed by the channel.
func (i *Item[T]) ValueWithAck() (T, AckFunc) {
	return i.value, func(s bool) {
		i.success <- s
	}
}
