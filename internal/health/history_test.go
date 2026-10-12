package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stretchr/testify/require"
)

type historyRecorderStub struct {
	recorded    []domain.DependencyHealthCheck
	pruneBefore time.Time
}

func (r *historyRecorderStub) RecordDependencyHealth(_ context.Context, checks []domain.DependencyHealthCheck) error {
	r.recorded = append([]domain.DependencyHealthCheck(nil), checks...)
	return nil
}
func (r *historyRecorderStub) PruneDependencyHealth(_ context.Context, before time.Time) error {
	r.pruneBefore = before
	return nil
}

func TestSamplerPersistsSanitizedStatusAndRetentionCutoff(t *testing.T) {
	recorder := &historyRecorderStub{}
	sampler := NewSampler(map[string]DependencyCheck{"redis": func(context.Context) error { return errors.New("credential=secret") }}, recorder)
	require.NoError(t, sampler.SampleNow(context.Background()))
	require.Len(t, recorder.recorded, 1)
	require.Equal(t, "redis", recorder.recorded[0].Dependency)
	require.Equal(t, "unhealthy", recorder.recorded[0].Status)
	require.NotContains(t, recorder.recorded[0].Dependency, "secret")
	require.GreaterOrEqual(t, recorder.recorded[0].LatencyMS, int64(0))
	require.WithinDuration(t, time.Now().UTC(), recorder.recorded[0].CheckedAt, time.Second)
	require.WithinDuration(t, time.Now().UTC().Add(-HistoryRetention), recorder.pruneBefore, time.Second)
}
