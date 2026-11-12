package clienttinc

import (
	"context"
	"time"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

const readinessRetryInterval = 500 * time.Millisecond

type readinessObservation struct {
	ingresses []string
	ready     bool
	message   string
}

// The owner loop runs one probe at a time. Stop cancels it through the parent
// context; the existing health timeout and authorized grant deadline still apply.
func probePathReadiness(ctx context.Context, expiresAt time.Time, ingress func(context.Context) []string, health func(context.Context) error) (readinessObservation, bool) {
	ctx, cancel := context.WithDeadline(ctx, expiresAt)
	defer cancel()
	valid := func() bool { return ctx.Err() == nil && time.Now().Before(expiresAt) }
	observation := readinessObservation{message: "Waiting for an ingress"}
	if !valid() {
		return observation, false
	}
	observation.ingresses = ingress(ctx)
	if !valid() {
		return observation, false
	}
	if len(observation.ingresses) > 0 {
		observation.message = "Waiting for the authorized exit"
		if health == nil || health(ctx) == nil {
			observation.ready, observation.message = true, ""
		}
	}
	return observation, valid()
}

func readinessStatus(previous Status, config wire.ClientConfig, desired string, observation readinessObservation, apiError error) Status {
	previous.State = "connecting"
	if observation.ready {
		previous.State = "connected"
	}
	previous.Country = config.Assignment.Country
	previous.DesiredCountry = desired
	previous.EgressID = config.Assignment.EgressID.String()
	previous.AssignmentVersion = config.Assignment.Version
	previous.ExpiresAt = config.ExpiresAt
	previous.Ingresses = append([]string(nil), observation.ingresses...)
	// A local successful packet is not a new authorization response. In
	// particular it must not hide capacity/refusal reasons or renew a lease.
	if apiError == nil {
		previous.Code, previous.RetryAfter = "", 0
		previous.Message = observation.message
	}
	return previous
}
