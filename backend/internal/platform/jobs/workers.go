package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/riverqueue/river"
)

const metadataVersion = `{"v":1}`

type ingressWorker struct {
	river.WorkerDefaults[IngressArgs]
	handler IngressHandler
}

func (w *ingressWorker) Work(ctx context.Context, job *river.Job[IngressArgs]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := job.Args.validate(); err != nil || !metadataOK(job.Metadata) {
		return river.JobCancel(errInvalidArgs)
	}
	if w.handler == nil {
		return errHandlerMissing
	}
	return w.handler.ExpandIngress(ctx, job.Args.DeliveryID)
}

type inboundWorker struct {
	river.WorkerDefaults[InboundArgs]
	handler InboundHandler
}

func (w *inboundWorker) Work(ctx context.Context, job *river.Job[InboundArgs]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := job.Args.validate(); err != nil || !metadataOK(job.Metadata) {
		return river.JobCancel(errInvalidArgs)
	}
	if w.handler == nil {
		return errHandlerMissing
	}
	return w.handler.ApplyInbound(ctx, job.Args.TenantID, job.Args.ItemID)
}

type outboundWorker struct {
	river.WorkerDefaults[OutboundArgs]
	handler OutboundHandler
	lease   OutboundLease
	wait    time.Duration
}

func (w *outboundWorker) Work(ctx context.Context, job *river.Job[OutboundArgs]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := job.Args.validate(); err != nil || !metadataOK(job.Metadata) {
		return river.JobCancel(errInvalidArgs)
	}
	if w.lease == nil {
		return errLeaseMissing
	}
	allowed, err := w.lease.AllowOutbound(ctx, job.Args.TenantID, job.Args.AttemptID)
	if err != nil {
		return err
	}
	if !allowed {
		wait := w.wait
		if wait <= 0 {
			wait = LeaseSnooze
		}
		return river.JobSnooze(wait)
	}
	if w.handler == nil {
		return errHandlerMissing
	}
	return w.handler.DispatchOutbound(ctx, job.Args.TenantID, job.Args.AttemptID)
}

func metadataOK(raw []byte) bool {
	var meta struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return false
	}
	return meta.V == SchemaVersion
}

func (IngressArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueIngress, Priority: priorityIngress, MaxAttempts: MaxAttempts, Metadata: []byte(metadataVersion)}
}

func (InboundArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueInbound, Priority: priorityInbound, MaxAttempts: MaxAttempts, Metadata: []byte(metadataVersion)}
}

func (OutboundArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueOutbound, Priority: priorityOutbound, MaxAttempts: MaxAttempts, Metadata: []byte(metadataVersion)}
}

// AutomationArgs carries no customer data. Tenant enumeration uses platform metadata.
type AutomationArgs struct {
	V int `json:"v"`
}

func (AutomationArgs) Kind() string { return "chatisp.automation.tick.v1" }
func (AutomationArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: "scheduled", MaxAttempts: MaxAttempts, Metadata: []byte(metadataVersion)}
}

type automationWorker struct {
	river.WorkerDefaults[AutomationArgs]
	handler AutomationHandler
}

func (w *automationWorker) Work(ctx context.Context, job *river.Job[AutomationArgs]) error {
	if job.Args.V != SchemaVersion || !metadataOK(job.Metadata) {
		return river.JobCancel(errInvalidArgs)
	}
	if w.handler == nil {
		return errHandlerMissing
	}
	return w.handler.RunAutomations(ctx)
}
