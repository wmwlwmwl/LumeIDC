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
	Category  string
	Summary   string
	Content   string
	Cover     string
	Hidden    bool
	Pinned    bool
	Reads     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Announcements struct{ db *sql.DB }

const announcementCols = `id,title,category,summary,content,cover,hidden,pinned,reads,created_at,updated_at`

// List 后台取全部；前台 activeOnly=true 仅取显示中（hidden=false）。pinned 置顶。
func (a *Announcements) List(ctx context.Context, activeOnly bool) ([]Announcement, error) {
	q := `SELECT ` + announcementCols + ` FROM announcements`
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
		if err := rows.Scan(&an.ID, &an.Title, &an.Category, &an.Summary, &an.Content, &an.Cover,
			&an.Hidden, &an.Pinned, &an.Reads, &an.CreatedAt, &an.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, an)
	}
	return out, rows.Err()
}

func (a *Announcements) Get(ctx context.Context, id int64) (*Announcement, error) {
	var an Announcement
	err := a.db.QueryRowContext(ctx,
		`SELECT `+announcementCols+` FROM announcements WHERE id=$1`, id).
		Scan(&an.ID, &an.Title, &an.Category, &an.Summary, &an.Content, &an.Cover,
			&an.Hidden, &an.Pinned, &an.Reads, &an.CreatedAt, &an.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &an, err
}

// Save 新增(id=0)或更新。hidden/pinned 控制显示与置顶。
func (a *Announcements) Save(ctx context.Context, id int64, title, category, summary, content, cover string, hidden, pinned bool) error {
	if id == 0 {
		_, err := a.db.ExecContext(ctx,
			`INSERT INTO announcements(title,category,summary,content,cover,hidden,pinned) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			title, category, summary, content, cover, hidden, pinned)
		return err
	}
	_, err := a.db.ExecContext(ctx,
		`UPDATE announcements SET title=$2,category=$3,summary=$4,content=$5,cover=$6,hidden=$7,pinned=$8,updated_at=now() WHERE id=$1`,
		id, title, category, summary, content, cover, hidden, pinned)
	return err
}

// IncRead 阅读量 +1。
func (a *Announcements) IncRead(ctx context.Context, id int64) error {
	_, err := a.db.ExecContext(ctx, `UPDATE announcements SET reads=reads+1 WHERE id=$1`, id)
	return err
}

func (a *Announcements) Delete(ctx context.Context, id int64) error {
	_, err := a.db.ExecContext(ctx, `DELETE FROM announcements WHERE id=$1`, id)
	return err
}
