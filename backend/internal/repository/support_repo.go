package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/vincommerce/backend/internal/db"
	"github.com/vincommerce/backend/internal/domain"
)

// SupportRepository persists help content and support tickets.
type SupportRepository struct {
	pool *db.Pool
}

// NewSupportRepository creates a SupportRepository.
func NewSupportRepository(pool *db.Pool) *SupportRepository {
	return &SupportRepository{pool: pool}
}

// --- help categories ---

// CreateCategory inserts a help category.
func (r *SupportRepository) CreateCategory(ctx context.Context, c *domain.HelpCategory) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO help_categories (id, name, slug, position) VALUES ($1, $2, $3, $4)`,
		c.ID, c.Name, c.Slug, c.Position)
	return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "category slug is taken"), nil)
}

// Categories lists help categories.
func (r *SupportRepository) Categories(ctx context.Context) ([]*domain.HelpCategory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, slug, position, is_active FROM help_categories ORDER BY position, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.HelpCategory{}
	for rows.Next() {
		var c domain.HelpCategory
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.Position, &c.IsActive); err != nil {
			return nil, err
		}
		items = append(items, &c)
	}
	return items, rows.Err()
}

// --- help articles ---

// CreateArticle inserts a help article.
func (r *SupportRepository) CreateArticle(ctx context.Context, a *domain.HelpArticle) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO help_articles (id, category_id, title, slug, excerpt, content, section, is_published, published_at)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8,
		        CASE WHEN $8 THEN now() ELSE NULL END)
		RETURNING created_at, updated_at`,
		a.ID, a.CategoryID, a.Title, a.Slug, a.Excerpt, a.Content, a.Section, a.IsPublished).
		Scan(&a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "article slug is taken"), nil)
	}
	return nil
}

// UpdateArticle edits an article (admin).
func (r *SupportRepository) UpdateArticle(ctx context.Context, a *domain.HelpArticle) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE help_articles SET category_id = NULLIF($2, '')::uuid, title = $3, slug = $4,
			excerpt = $5, content = $6, section = $7, is_published = $8, updated_at = now(),
			published_at = CASE WHEN $8 THEN COALESCE(published_at, now()) ELSE published_at END
		WHERE id = $1`,
		a.ID, a.CategoryID, a.Title, a.Slug, a.Excerpt, a.Content, a.Section, a.IsPublished)
	if err != nil {
		return mapPgConflict(err, domain.E(domain.KindConflict, "SLUG_TAKEN", "article slug is taken"), nil)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Articles lists published articles for a section, optionally by category.
func (r *SupportRepository) Articles(ctx context.Context, section string, categorySlug string, search string) ([]*domain.HelpArticle, error) {
	where := `a.is_published = TRUE`
	args := []any{}
	if section != "" {
		args = append(args, section)
		where += ` AND a.section = $` + itoa(len(args))
	}
	if categorySlug != "" {
		args = append(args, categorySlug)
		where += ` AND c.slug = $` + itoa(len(args))
	}
	if search != "" {
		args = append(args, search)
		where += ` AND to_tsvector('english', a.title || ' ' || a.excerpt || ' ' || a.content) @@ websearch_to_tsquery('english', $` + itoa(len(args)) + `)`
	}

	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.category_id, a.title, a.slug, a.excerpt, a.content, a.section,
		       a.is_published, a.view_count, a.published_at, a.created_at, a.updated_at,
		       COALESCE(c.name, ''), COALESCE(c.slug, '')
		FROM help_articles a
		LEFT JOIN help_categories c ON c.id = a.category_id
		WHERE `+where+`
		ORDER BY a.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.HelpArticle{}
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// ArticleBySlug fetches a published article and increments its view count.
func (r *SupportRepository) ArticleBySlug(ctx context.Context, slug string) (*domain.HelpArticle, error) {
	a, err := r.articleBy(ctx, `WHERE a.slug = $1 AND a.is_published = TRUE`, slug)
	if err != nil {
		return nil, err
	}
	_, err = r.pool.Exec(ctx, `UPDATE help_articles SET view_count = view_count + 1 WHERE id = $1`, a.ID)
	return a, err
}

// AllArticles fetches every article regardless of publication (admin).
func (r *SupportRepository) AllArticles(ctx context.Context) ([]*domain.HelpArticle, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.category_id, a.title, a.slug, a.excerpt, a.content, a.section,
		       a.is_published, a.view_count, a.published_at, a.created_at, a.updated_at,
		       COALESCE(c.name, ''), COALESCE(c.slug, '')
		FROM help_articles a
		LEFT JOIN help_categories c ON c.id = a.category_id
		ORDER BY a.section, a.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.HelpArticle{}
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// DeleteArticle removes an article (admin).
func (r *SupportRepository) DeleteArticle(ctx context.Context, articleID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM help_articles WHERE id = $1`, articleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *SupportRepository) articleBy(ctx context.Context, where string, arg any) (*domain.HelpArticle, error) {
	var a domain.HelpArticle
	var catName, catSlug string
	err := r.pool.QueryRow(ctx, `
		SELECT a.id, a.category_id, a.title, a.slug, a.excerpt, a.content, a.section,
		       a.is_published, a.view_count, a.published_at, a.created_at, a.updated_at,
		       COALESCE(c.name, ''), COALESCE(c.slug, '')
		FROM help_articles a
		LEFT JOIN help_categories c ON c.id = a.category_id
		`+where, arg).
		Scan(&a.ID, &a.CategoryID, &a.Title, &a.Slug, &a.Excerpt, &a.Content, &a.Section,
			&a.IsPublished, &a.ViewCount, &a.PublishedAt, &a.CreatedAt, &a.UpdatedAt, &catName, &catSlug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if catName != "" {
		a.Category = &domain.HelpCategory{Name: catName, Slug: catSlug}
	}
	return &a, nil
}

type articleRow interface {
	Scan(dest ...any) error
}

func scanArticle(row articleRow) (*domain.HelpArticle, error) {
	var a domain.HelpArticle
	var catName, catSlug string
	err := row.Scan(&a.ID, &a.CategoryID, &a.Title, &a.Slug, &a.Excerpt, &a.Content, &a.Section,
		&a.IsPublished, &a.ViewCount, &a.PublishedAt, &a.CreatedAt, &a.UpdatedAt, &catName, &catSlug)
	if err != nil {
		return nil, err
	}
	if catName != "" {
		a.Category = &domain.HelpCategory{Name: catName, Slug: catSlug}
	}
	return &a, nil
}

// --- tickets ---

// CreateTicket opens a support ticket.
func (r *SupportRepository) CreateTicket(ctx context.Context, t *domain.SupportTicket) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO support_tickets (id, ticket_number, user_id, order_id, subject, category, priority)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7)
		RETURNING created_at`,
		t.ID, t.TicketNumber, t.UserID, t.OrderID, t.Subject, t.Category, t.Priority).
		Scan(&t.CreatedAt)
	return err
}

// AddMessage appends a ticket message and bumps the ticket.
func (r *SupportRepository) AddMessage(ctx context.Context, m *domain.TicketMessage) error {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO ticket_messages (ticket_id, author_id, author_role, body, is_internal)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		m.TicketID, m.AuthorID, m.AuthorRole, m.Body, m.IsInternal).Scan(&m.CreatedAt)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE support_tickets SET updated_at = now()
		WHERE id = $1`, m.TicketID)
	return err
}

// TicketByID loads a ticket with the last customer message.
func (r *SupportRepository) TicketByID(ctx context.Context, ticketID string) (*domain.SupportTicket, error) {
	var t domain.SupportTicket
	err := r.pool.QueryRow(ctx, `
		SELECT t.id, t.ticket_number, t.user_id, t.order_id, t.subject, t.category, t.priority,
		       t.status, t.assigned_to, t.created_at, t.updated_at, t.resolved_at,
		       COALESCE((SELECT body FROM ticket_messages m WHERE m.ticket_id = t.id AND m.is_internal = FALSE ORDER BY m.created_at DESC LIMIT 1), '')
		FROM support_tickets t
		WHERE t.id = $1`, ticketID).
		Scan(&t.ID, &t.TicketNumber, &t.UserID, &t.OrderID, &t.Subject, &t.Category, &t.Priority,
			&t.Status, &t.AssignedTo, &t.CreatedAt, &t.UpdatedAt, &t.ResolvedAt, &t.LastMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

// Messages lists the thread (staff sees internal notes, customers only public).
func (r *SupportRepository) Messages(ctx context.Context, ticketID string, staff bool) ([]*domain.TicketMessage, error) {
	where := `m.ticket_id = $1`
	if !staff {
		where += ` AND m.is_internal = FALSE`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.ticket_id, m.author_id, m.author_role, m.body, m.is_internal, m.created_at, u.full_name
		FROM ticket_messages m
		JOIN users u ON u.id = m.author_id
		WHERE `+where+`
		ORDER BY m.created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.TicketMessage{}
	for rows.Next() {
		var m domain.TicketMessage
		if err := rows.Scan(&m.ID, &m.TicketID, &m.AuthorID, &m.AuthorRole, &m.Body, &m.IsInternal, &m.CreatedAt, &m.AuthorName); err != nil {
			return nil, err
		}
		items = append(items, &m)
	}
	return items, rows.Err()
}

// TicketsByUser lists a customer's tickets.
func (r *SupportRepository) TicketsByUser(ctx context.Context, userID string) ([]*domain.SupportTicket, error) {
	return r.tickets(ctx, `WHERE t.user_id = $1`, userID)
}

// TicketsByStatus lists tickets by status (staff queue).
func (r *SupportRepository) TicketsByStatus(ctx context.Context, status string) ([]*domain.SupportTicket, error) {
	where := `t.status <> 'closed'`
	var arg any
	if status != "" && status != "all" {
		where = `t.status = $1`
		arg = status
	}
	return r.tickets(ctx, `WHERE `+where, arg)
}

func (r *SupportRepository) tickets(ctx context.Context, where string, arg any) ([]*domain.SupportTicket, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if arg == nil {
		rows, err = r.pool.Query(ctx, `
			SELECT t.id, t.ticket_number, t.user_id, t.order_id, t.subject, t.category, t.priority,
			       t.status, t.assigned_to, t.created_at, t.updated_at, t.resolved_at,
			       COALESCE((SELECT body FROM ticket_messages m WHERE m.ticket_id = t.id AND m.is_internal = FALSE ORDER BY m.created_at DESC LIMIT 1), '')
			FROM support_tickets t
			`+where+`
			ORDER BY t.updated_at DESC`)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT t.id, t.ticket_number, t.user_id, t.order_id, t.subject, t.category, t.priority,
			       t.status, t.assigned_to, t.created_at, t.updated_at, t.resolved_at,
			       COALESCE((SELECT body FROM ticket_messages m WHERE m.ticket_id = t.id AND m.is_internal = FALSE ORDER BY m.created_at DESC LIMIT 1), '')
			FROM support_tickets t
			`+where+`
			ORDER BY t.updated_at DESC`, arg)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []*domain.SupportTicket{}
	for rows.Next() {
		var t domain.SupportTicket
		if err := rows.Scan(&t.ID, &t.TicketNumber, &t.UserID, &t.OrderID, &t.Subject, &t.Category, &t.Priority,
			&t.Status, &t.AssignedTo, &t.CreatedAt, &t.UpdatedAt, &t.ResolvedAt, &t.LastMessage); err != nil {
			return nil, err
		}
		items = append(items, &t)
	}
	return items, rows.Err()
}

// UpdateTicketStatus transitions a ticket.
func (r *SupportRepository) UpdateTicketStatus(ctx context.Context, ticketID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE support_tickets SET status = $2::varchar, updated_at = now(),
			resolved_at = CASE WHEN $2::varchar IN ('resolved','closed') THEN now() ELSE resolved_at END
		WHERE id = $1`, ticketID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AssignTicket sets the handling agent.
func (r *SupportRepository) AssignTicket(ctx context.Context, ticketID, agentID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE support_tickets SET assigned_to = NULLIF($2, '')::uuid, updated_at = now()
		WHERE id = $1`, ticketID, agentID)
	return err
}

// NextTicketNumber fetches the next ticket sequence value.
func (r *SupportRepository) NextTicketNumber(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT nextval('ticket_number_seq')`).Scan(&n)
	return n, err
}
