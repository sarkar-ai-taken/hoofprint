// Package postgres is the production Store, backed by PostgreSQL via pgx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sarkar-ai-taken/hoofprint/internal/model"
	"github.com/sarkar-ai-taken/hoofprint/internal/store"
)

type Store struct {
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// Open connects to dsn, applies pending migrations and returns the store.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

func (s *Store) UpsertAgent(ctx context.Context, a *model.Agent) error {
	tags, err := json.Marshal(nonNilMap(a.Tags))
	if err != nil {
		return err
	}
	caps := a.Capabilities
	if caps == nil {
		caps = []string{}
	}
	status := a.Status
	if status == "" {
		status = model.AgentOffline
	}
	var hb *time.Time
	if !a.LastHeartbeat.IsZero() {
		hb = &a.LastHeartbeat
	}
	var registered any = a.RegisteredAt
	if a.RegisteredAt.IsZero() {
		registered = time.Now().UTC()
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO agents (id, cluster, version, capabilities, tags, status, registered_at, last_heartbeat)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			cluster = EXCLUDED.cluster,
			version = EXCLUDED.version,
			capabilities = EXCLUDED.capabilities,
			tags = EXCLUDED.tags,
			status = EXCLUDED.status,
			last_heartbeat = COALESCE(EXCLUDED.last_heartbeat, agents.last_heartbeat)`,
		a.ID, a.Cluster, a.Version, caps, tags, string(status), registered, hb)
	return err
}

const agentCols = `id, cluster, version, capabilities, tags, status, registered_at, last_heartbeat`

func scanAgent(row pgx.Row) (*model.Agent, error) {
	var a model.Agent
	var tags []byte
	var status string
	var hb *time.Time
	if err := row.Scan(&a.ID, &a.Cluster, &a.Version, &a.Capabilities, &tags, &status, &a.RegisteredAt, &hb); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	a.Status = model.AgentStatus(status)
	if hb != nil {
		a.LastHeartbeat = *hb
	}
	if err := json.Unmarshal(tags, &a.Tags); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (*model.Agent, error) {
	return scanAgent(s.pool.QueryRow(ctx, `SELECT `+agentCols+` FROM agents WHERE id = $1`, id))
}

func (s *Store) ListAgents(ctx context.Context) ([]*model.Agent, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+agentCols+` FROM agents ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SetAgentStatus(ctx context.Context, id string, status model.AgentStatus) error {
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET status = $2 WHERE id = $1`, id, string(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) TouchAgent(ctx context.Context, id string) error {
	// Stamp with the process clock, not the database's now(): the control
	// plane compares heartbeats against its own clock in the reaper.
	tag, err := s.pool.Exec(ctx, `UPDATE agents SET status = 'online', last_heartbeat = $2 WHERE id = $1`, id, time.Now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) InsertDeploy(ctx context.Context, d *model.Deploy) error {
	ib, _ := json.Marshal(nonNilImages(d.ImagesBefore))
	ia, _ := json.Marshal(nonNilImages(d.ImagesAfter))
	labels, _ := json.Marshal(nonNilMap(d.Labels))
	ann, _ := json.Marshal(nonNilMap(d.Annotations))
	created := d.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deploys (id, agent_id, cluster, namespace, kind, name, tier, controller,
			images_before, images_after, template_hash_before, template_hash_after,
			revision_before, revision_after, labels, annotations, observed_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		d.ID, d.AgentID, d.Cluster, d.Namespace, d.Kind, d.Name, int16(d.Tier), d.Controller,
		ib, ia, d.TemplateHashBefore, d.TemplateHashAfter,
		d.RevisionBefore, d.RevisionAfter, labels, ann, d.ObservedAt, created)
	return err
}

const deployCols = `id, agent_id, cluster, namespace, kind, name, tier, controller,
	images_before, images_after, template_hash_before, template_hash_after,
	revision_before, revision_after, labels, annotations, observed_at, created_at`

func scanDeploy(row pgx.Row) (*model.Deploy, error) {
	var d model.Deploy
	var tier int16
	var ib, ia, labels, ann []byte
	if err := row.Scan(&d.ID, &d.AgentID, &d.Cluster, &d.Namespace, &d.Kind, &d.Name, &tier, &d.Controller,
		&ib, &ia, &d.TemplateHashBefore, &d.TemplateHashAfter,
		&d.RevisionBefore, &d.RevisionAfter, &labels, &ann, &d.ObservedAt, &d.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	d.Tier = model.Tier(tier)
	for _, p := range []struct {
		b []byte
		v any
	}{{ib, &d.ImagesBefore}, {ia, &d.ImagesAfter}, {labels, &d.Labels}, {ann, &d.Annotations}} {
		if err := json.Unmarshal(p.b, p.v); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

func (s *Store) GetDeploy(ctx context.Context, id string) (*model.Deploy, error) {
	return scanDeploy(s.pool.QueryRow(ctx, `SELECT `+deployCols+` FROM deploys WHERE id = $1`, id))
}

func (s *Store) ListDeploys(ctx context.Context, f model.DeployFilter) ([]*model.Deploy, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `SELECT `+deployCols+` FROM deploys
		WHERE ($1 = '' OR cluster = $1) AND ($2 = '' OR namespace = $2)
		ORDER BY observed_at DESC, id DESC LIMIT $3`, f.Cluster, f.Namespace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Deploy{}
	for rows.Next() {
		d, err := scanDeploy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) InsertVerdict(ctx context.Context, v *model.Verdict, evidence []*model.Evidence) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	created := v.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO verdicts (id, deploy_id, judge, outcome, confidence, rationale, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		v.ID, v.DeployID, v.Judge, string(v.Outcome), v.Confidence, v.Rationale, created); err != nil {
		return err
	}
	for _, e := range evidence {
		ec := e.CreatedAt
		if ec.IsZero() {
			ec = created
		}
		payload := e.Payload
		if payload == nil {
			payload = []byte{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO evidence (id, verdict_id, kind, title, payload, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, e.ID, v.ID, e.Kind, e.Title, payload, ec); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const verdictCols = `id, deploy_id, judge, outcome, confidence, rationale, created_at`

func scanVerdict(row pgx.Row) (*model.Verdict, error) {
	var v model.Verdict
	var outcome string
	if err := row.Scan(&v.ID, &v.DeployID, &v.Judge, &outcome, &v.Confidence, &v.Rationale, &v.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	v.Outcome = model.Outcome(outcome)
	return &v, nil
}

func (s *Store) GetVerdict(ctx context.Context, id string) (*model.Verdict, []*model.Evidence, error) {
	v, err := scanVerdict(s.pool.QueryRow(ctx, `SELECT `+verdictCols+` FROM verdicts WHERE id = $1`, id))
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, verdict_id, kind, title, payload, created_at FROM evidence WHERE verdict_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	evs := []*model.Evidence{}
	for rows.Next() {
		var e model.Evidence
		if err := rows.Scan(&e.ID, &e.VerdictID, &e.Kind, &e.Title, &e.Payload, &e.CreatedAt); err != nil {
			return nil, nil, err
		}
		evs = append(evs, &e)
	}
	return v, evs, rows.Err()
}

func (s *Store) ListVerdicts(ctx context.Context, deployID string) ([]*model.Verdict, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+verdictCols+` FROM verdicts
		WHERE ($1 = '' OR deploy_id = $1) ORDER BY created_at DESC, id DESC`, deployID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*model.Verdict{}
	for rows.Next() {
		v, err := scanVerdict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonNilImages(i []model.ContainerImage) []model.ContainerImage {
	if i == nil {
		return []model.ContainerImage{}
	}
	return i
}
