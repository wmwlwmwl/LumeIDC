package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Announcement struct {
	ID        int64
	Title     string
	Content   string
	Hidden    bool
	Pinned    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Announcements struct{ db *sql.DB }

// List 后台取全部；前台 activeOnly=true 仅取显示中（hidden=false）。pinned 置顶。
func (a *Announcements) List(ctx context.Context, activeOnly bool) ([]Announcement, error) {
	q := `SELECT id,title,content,hidden,pinned,created_at,updated_at FROM announcements`
	if activeOnly {
		q += ` WHERE hidden=false`
	}
	q += ` ORDER BY pinned DESC, id DESC`
	rows, err := a.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var an Announcement
		if err := rows.Scan(&an.ID, &an.Title, &an.Content, &an.Hidden, &an.Pinned, &an.CreatedAt, &an.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, an)
	}
	return out, rows.Err()
}

func (a *Announcements) Get(ctx context.Context, id int64) (*Announcement, error) {
	var an Announcement
	err := a.db.QueryRowContext(ctx,
		`SELECT id,title,content,hidden,pinned,created_at,updated_at FROM announcements WHERE id=$1`, id).
		Scan(&an.ID, &an.Title, &an.Content, &an.Hidden, &an.Pinned, &an.CreatedAt, &an.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &an, err
}

// Save 新增(id=0)或更新。hidden/pinned 控制显示与置顶。
func (a *Announcements) Save(ctx context.Context, id int64, title, content string, hidden, pinned bool) error {
	if id == 0 {
		_, err := a.db.ExecContext(ctx,
			`INSERT INTO announcements(title,content,hidden,pinned) VALUES($1,$2,$3,$4)`,
			title, content, hidden, pinned)
		return err
	}
	_, err := a.db.ExecContext(ctx,
		`UPDATE announcements SET title=$2,content=$3,hidden=$4,pinned=$5,updated_at=now() WHERE id=$1`,
		id, title, content, hidden, pinned)
	return err
}

func (a *Announcements) Delete(ctx context.Context, id int64) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM announcements WHERE id=$1`, id)
	return err
}
