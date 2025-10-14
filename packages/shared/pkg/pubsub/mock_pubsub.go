package pubsub

import "context"

type MockPubSub[PayloadT, SubMetaDataT any] struct {
	// Mock fields for testing
	shouldPublish bool
	metadata      map[string]SubMetaDataT
}

func NewMockPubSub[PayloadT, SubMetaDataT any]() *MockPubSub[PayloadT, SubMetaDataT] {
	return &MockPubSub[PayloadT, SubMetaDataT]{
		shouldPublish: true,
		metadata:      make(map[string]SubMetaDataT),
	}
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) Publish(context.Context, PayloadT) error {
	// Mock implementation - does nothing
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) Subscribe(context.Context, chan<- PayloadT) error {
	// Mock implementation - does nothing
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) ShouldPublish(context.Context, string) (bool, error) {
	// Mock implementation - returns the configured shouldPublish value
	return m.shouldPublish, nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) GetSubMetaData(_ context.Context, key string) (SubMetaDataT, error) {
	var metadata SubMetaDataT
	// Mock implementation - returns metadata if it exists
	if meta, exists := m.metadata[key]; exists {
		return meta, nil
	}
	return metadata, nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) SetSubMetaData(_ context.Context, key string, metaData SubMetaDataT) error {
	// Mock implementation - stores metadata
	if m.metadata == nil {
		m.metadata = make(map[string]SubMetaDataT)
	}
	m.metadata[key] = metaData
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) DeleteSubMetaData(_ context.Context, key string) error {
	// Mock implementation - deletes metadata
	delete(m.metadata, key)
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) Close(context.Context) error {
	// Mock implementation - does nothing
	return nil
}

// SetShouldPublish allows setting the mock's shouldPublish behavior
func (m *MockPubSub[PayloadT, SubMetaDataT]) SetShouldPublish(should bool) {
	m.shouldPublish = should
}
