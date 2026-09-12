package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

// Access records who asked for the console, who has it, and who had it.
//
// The invite store is in memory and the request form only sent an email, so
// after a restart nobody could say who had access and nobody could ever say
// who had it before. An access decision outlives the process that made it, so
// it belongs here.

// Grant statuses. A row never leaves the table: ending access is a status
// change plus a reason, which is what keeps the history answerable.
const (
	StatusRequested = "requested"
	StatusGranted   = "granted"
	StatusRevoked   = "revoked"
	StatusExpired   = "expired"
)

// Grant is one person's relationship with the console, over its whole life.
type Grant struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Company     string     `json:"company,omitempty"`
	UseCase     string     `json:"use_case,omitempty"`
	Status      string     `json:"status"`
	Username    string     `json:"username,omitempty"`
	InviteCode  string     `json:"invite_code,omitempty"`
	RequestedAt time.Time  `json:"requested_at"`
	GrantedAt   *time.Time `json:"granted_at,omitempty"`
	GrantedBy   string     `json:"granted_by,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	RevokedBy   string     `json:"revoked_by,omitempty"`
	RevokedCode string     `json:"revoked_code,omitempty"`
	RevokedNote string     `json:"revoked_note,omitempty"`
	ScreenScore int        `json:"screen_score"`
	ScreenNote  string     `json:"screen_note,omitempty"`
}

// Active reports whether this grant currently permits sign-in. Expiry is
// computed rather than stored as a status, so a grant does not need a sweeper
// to become correct.
func (g Grant) Active() bool {
	if g.Status != StatusGranted {
		return false
	}
	if g.ExpiresAt != nil && time.Now().After(*g.ExpiresAt) {
		return false
	}
	return true
}

// AccessEvent is one entry in the append-only history of a grant.
type AccessEvent struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Detail string    `json:"detail,omitempty"`
}

func newGrantID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return "g-" + strings.ToLower(
		base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

// RecordRequest stores an access request. Requests are recorded even when
// screening rejects them: knowing what was refused, and why, is the half of
// the picture a dashboard of granted users cannot show.
func (p *Postgres) RecordRequest(ctx context.Context, g Grant) (Grant, error) {
	if g.ID == "" {
		g.ID = newGrantID()
	}
	if g.Status == "" {
		g.Status = StatusRequested
	}
	g.RequestedAt = time.Now().UTC()

	_, err := p.pool.Exec(ctx, `
		INSERT INTO access_grants
		  (id, tenant_id, email, name, company, use_case, status,
		   requested_at, screen_score, screen_note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		g.ID, p.tenant, strings.ToLower(g.Email), g.Name, g.Company, g.UseCase,
		g.Status, g.RequestedAt, g.ScreenScore, g.ScreenNote)
	if err != nil {
		return Grant{}, fmt.Errorf("record access request: %w", err)
	}
	return g, p.logAccess(ctx, g.ID, "system", "requested", g.ScreenNote)
}

// GrantAccess marks a request granted and attaches the invite issued for it.
func (p *Postgres) GrantAccess(ctx context.Context, id, by, code string, expires time.Time) error {
	now := time.Now().UTC()
	tag, err := p.pool.Exec(ctx, `
		UPDATE access_grants
		   SET status = $1, granted_at = $2, granted_by = $3,
		       invite_code = $4, expires_at = $5,
		       revoked_at = NULL, revoked_by = '', revoked_code = '', revoked_note = ''
		 WHERE id = $6`,
		StatusGranted, now, by, code, expires, id)
	if err != nil {
		return fmt.Errorf("grant access: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("grant access: no such request %q", id)
	}
	return p.logAccess(ctx, id, by, "granted",
		fmt.Sprintf("expires %s", expires.UTC().Format(time.RFC3339)))
}

// Redeemed links a grant to the account the invite actually created.
func (p *Postgres) Redeemed(ctx context.Context, code, username string) error {
	tag, err := p.pool.Exec(ctx,
		`UPDATE access_grants SET username = $1 WHERE invite_code = $2`,
		username, code)
	if err != nil {
		return fmt.Errorf("link redeemed invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// An invite minted outside this flow — by the CLI, say. Not an error:
		// the account is real, we simply have no request behind it.
		return nil
	}
	var id string
	if err := p.pool.QueryRow(ctx,
		`SELECT id FROM access_grants WHERE invite_code = $1`, code).Scan(&id); err != nil {
		return nil
	}
	return p.logAccess(ctx, id, username, "redeemed", "account created")
}

// Revoke ends access and records why. The clause is what the person is told;
// the note is for whoever reads this later.
func (p *Postgres) Revoke(ctx context.Context, id, by, clause, note string) (Grant, error) {
	now := time.Now().UTC()
	tag, err := p.pool.Exec(ctx, `
		UPDATE access_grants
		   SET status = $1, revoked_at = $2, revoked_by = $3,
		       revoked_code = $4, revoked_note = $5
		 WHERE id = $6`,
		StatusRevoked, now, by, clause, note, id)
	if err != nil {
		return Grant{}, fmt.Errorf("revoke access: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Grant{}, fmt.Errorf("revoke access: no such grant %q", id)
	}
	if err := p.logAccess(ctx, id, by, "revoked", clause+": "+note); err != nil {
		return Grant{}, err
	}
	return p.GrantByID(ctx, id)
}

// GrantByID reads one grant.
func (p *Postgres) GrantByID(ctx context.Context, id string) (Grant, error) {
	rows, _ := p.pool.Query(ctx, accessSelect+` WHERE id = $1`, id)
	gs, err := scanGrants(rows)
	if err != nil {
		return Grant{}, err
	}
	if len(gs) == 0 {
		return Grant{}, fmt.Errorf("no such grant %q", id)
	}
	return gs[0], nil
}

// GrantByUsername finds the grant behind an account, for the sign-in check.
func (p *Postgres) GrantByUsername(ctx context.Context, username string) (Grant, error) {
	rows, _ := p.pool.Query(ctx,
		accessSelect+` WHERE username = $1 ORDER BY requested_at DESC LIMIT 1`, username)
	gs, err := scanGrants(rows)
	if err != nil {
		return Grant{}, err
	}
	if len(gs) == 0 {
		return Grant{}, fmt.Errorf("no grant for %q", username)
	}
	return gs[0], nil
}

// Grants lists every grant, newest first. Deliberately unpaginated: this is a
// trial on one machine, and a dashboard that hides rows behind paging would
// answer "who has access" less well than a list that fits on a screen.
func (p *Postgres) Grants(ctx context.Context) ([]Grant, error) {
	rows, _ := p.pool.Query(ctx, accessSelect+` ORDER BY requested_at DESC`)
	return scanGrants(rows)
}

// AccessHistory returns the append-only trail for one grant.
func (p *Postgres) AccessHistory(ctx context.Context, id string) ([]AccessEvent, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT at, actor, action, detail FROM access_events
		  WHERE grant_id = $1 ORDER BY at`, id)
	if err != nil {
		return nil, fmt.Errorf("access history: %w", err)
	}
	defer rows.Close()
	var out []AccessEvent
	for rows.Next() {
		var e AccessEvent
		if err := rows.Scan(&e.At, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (p *Postgres) logAccess(ctx context.Context, grantID, actor, action, detail string) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO access_events (tenant_id, grant_id, actor, action, detail)
		 VALUES ($1, $2, $3, $4, $5)`,
		p.tenant, grantID, actor, action, detail)
	if err != nil {
		return fmt.Errorf("log access event: %w", err)
	}
	return nil
}

const accessSelect = `
	SELECT id, email, name, company, use_case, status, username, invite_code,
	       requested_at, granted_at, granted_by, expires_at,
	       revoked_at, revoked_by, revoked_code, revoked_note,
	       screen_score, screen_note
	  FROM access_grants`

type rowScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

func scanGrants(rows rowScanner) ([]Grant, error) {
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Email, &g.Name, &g.Company, &g.UseCase,
			&g.Status, &g.Username, &g.InviteCode, &g.RequestedAt,
			&g.GrantedAt, &g.GrantedBy, &g.ExpiresAt,
			&g.RevokedAt, &g.RevokedBy, &g.RevokedCode, &g.RevokedNote,
			&g.ScreenScore, &g.ScreenNote); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
