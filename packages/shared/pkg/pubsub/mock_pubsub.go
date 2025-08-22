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

func (m *MockPubSub[PayloadT, SubMetaDataT]) Publish(ctx context.Context, payload PayloadT) error {
	// Mock implementation - does nothing
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) Subscribe(ctx context.Context, pubSubQueue chan<- PayloadT) error {
	// Mock implementation - does nothing
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) ShouldPublish(ctx context.Context, key string) (bool, error) {
	// Mock implementation - returns the configured shouldPublish value
	return m.shouldPublish, nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) GetSubMetaData(ctx context.Context, key string) (SubMetaDataT, error) {
	var metadata SubMetaDataT
	// Mock implementation - returns metadata if it exists
	if meta, exists := m.metadata[key]; exists {
		return meta, nil
	}
	return metadata, nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) SetSubMetaData(ctx context.Context, key string, metaData SubMetaDataT) error {
	// Mock implementation - stores metadata
	if m.metadata == nil {
		m.metadata = make(map[string]SubMetaDataT)
	}
	m.metadata[key] = metaData
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) DeleteSubMetaData(ctx context.Context, key string) error {
	// Mock implementation - deletes metadata
	delete(m.metadata, key)
	return nil
}

func (m *MockPubSub[PayloadT, SubMetaDataT]) Close() error {
	// Mock implementation - does nothing
	return nil
}

// SetShouldPublish allows setting the mock's shouldPublish behavior
func (m *MockPubSub[PayloadT, SubMetaDataT]) SetShouldPublish(should bool) {
	m.shouldPublish = should
}
