package cron

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/redis"
)

// Job defines a cron task.
type Job struct {
	ID       string `yaml:"id"`
	Schedule string `yaml:"schedule"`
	Prompt   string `yaml:"prompt"`
}

// Daemon handles background scheduled execution of agent loops.
type Daemon struct {
	cron *cron.Cron
	jobs []Job

	// template is the fully configured gateway runner (catalog, security, skills).
	template        *agent.Runner
	log             *zap.Logger
	persistencePath string

	// Lock provides distributed locking so only one instance runs a job.
	// Nil = no distributed locking (local-only cron, legacy behavior).
	Lock *redis.CronLock

	mu sync.Mutex
}

// NewDaemon schedules jobs using runners cloned from template so cron jobs get the
// same tool catalog, guardrail, and audit behavior as interactive sessions.
func NewDaemon(
	jobs []Job,
	template *agent.Runner,
	logger *zap.Logger,
	persistencePath string,
) *Daemon {
	return &Daemon{
		cron: cron.New(
			cron.WithSeconds(),
			cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
		),
		jobs:            jobs,
		template:        template,
		log:             logger,
		persistencePath: persistencePath,
	}
}

// Start schedules all configured jobs and begins executing them.
// ctx controls the lifecycle of all jobs: cancelling it stops new job runs.
func (d *Daemon) Start(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	allJobs := append([]Job{}, d.jobs...)

	// Load persistent jobs
	if d.persistencePath != "" {
		if data, err := os.ReadFile(d.persistencePath); err == nil {
			var persisted []Job
			if err := json.Unmarshal(data, &persisted); err == nil {
				allJobs = append(allJobs, persisted...)
			}
		}
	}

	for _, j := range allJobs {
		job := j // capture loop variable
		_, err := d.cron.AddFunc(job.Schedule, func() {
			if ctx.Err() != nil {
				return
			}
			// Distributed lock: skip if another instance is running this job.
			if d.Lock != nil && d.Lock.Enabled() {
				lockCtx, lockCancel := context.WithTimeout(ctx, 5*time.Second)
				defer lockCancel()
				if !d.Lock.Lock(lockCtx, job.ID, 15*time.Minute) {
					d.log.Info("cron: job skipped (held by another instance)", zap.String("id", job.ID))
					return
				}
				defer func() {
					unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer unlockCancel()
					d.Lock.Unlock(unlockCtx, job.ID)
				}()
			}
			d.log.Info("cron: executing job", zap.String("id", job.ID))
			jobCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()

			if d.template == nil {
				d.log.Error("cron: no runner template", zap.String("id", job.ID))
				return
			}
			sid := "cron:" + job.ID
			runner := d.template.WithSession(sid)

			res, err := runner.Run(jobCtx, memory.Message{
				ID:        uuid.New().String(),
				SessionID: sid,
				Role:      memory.RoleUser,
				Content:   job.Prompt,
				CreatedAt: time.Now(),
			})
			if err != nil {
				d.log.Error("cron: job failed", zap.String("id", job.ID), zap.Error(err))
			} else {
				d.log.Info("cron: job finished", zap.String("id", job.ID), zap.Int("iterations", res.Iterations))
			}
		})
		if err != nil {
			d.log.Error("cron: failed to schedule job", zap.String("id", job.ID), zap.Error(err))
		} else {
			d.log.Info("cron: scheduled job", zap.String("id", job.ID), zap.String("schedule", job.Schedule))
		}
	}

	d.cron.Start()
	return nil
}

func (d *Daemon) Stop() {
	d.cron.Stop()
}
