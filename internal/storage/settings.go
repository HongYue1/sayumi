package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SettingsRecord struct {
	UserID              string
	FontSize            sql.NullInt64
	FontFamily          sql.NullString
	LineHeight          sql.NullFloat64
	ParagraphSpacing    sql.NullFloat64
	TextIndent          sql.NullFloat64
	ContentWidth        sql.NullInt64
	DisplayMode         sql.NullString
	MarginTop           sql.NullInt64
	MarginBottom        sql.NullInt64
	MarginSide          sql.NullInt64
	PreserveStyles      sql.NullBool
	PreserveFonts       sql.NullBool
	Justify             sql.NullBool
	Hyphenation         sql.NullBool
	Theme               sql.NullString
	ChapterTitleAlign   sql.NullString
	ChapterTitleSize    sql.NullInt64
	ChapterTitleSpacing sql.NullFloat64
	FontRoles           sql.NullString // JSON map: family id -> {regular,italic,bold}
	UpdatedAt           string
}

func (db *DB) GetSettingsContext(ctx context.Context, userID string) (SettingsRecord, error) {
	row := db.QueryRowContext(ctx, `
		SELECT user_id, font_size, font_family, line_height, paragraph_spacing,
		       text_indent, content_width, display_mode,
		       margin_top, margin_bottom, margin_side,
		       preserve_styles, preserve_fonts, justify, hyphenation, theme,
		       chapter_title_align, chapter_title_size, chapter_title_spacing,
		       font_roles, updated_at
		FROM settings
		WHERE user_id = ?
	`, userID)

	var settings SettingsRecord
	err := row.Scan(
		&settings.UserID, &settings.FontSize, &settings.FontFamily, &settings.LineHeight, &settings.ParagraphSpacing,
		&settings.TextIndent, &settings.ContentWidth, &settings.DisplayMode,
		&settings.MarginTop, &settings.MarginBottom, &settings.MarginSide,
		&settings.PreserveStyles, &settings.PreserveFonts, &settings.Justify, &settings.Hyphenation, &settings.Theme,
		&settings.ChapterTitleAlign, &settings.ChapterTitleSize, &settings.ChapterTitleSpacing,
		&settings.FontRoles, &settings.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return settings, ErrNotFound
		}
		return settings, fmt.Errorf("get settings: %w", err)
	}
	return settings, nil
}

func (db *DB) SaveSettingsContext(ctx context.Context, settings SettingsRecord) error {
	db.writeMu.Lock()
	defer db.writeMu.Unlock()

	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(
		ctx, `
		INSERT INTO settings (
			user_id, font_size, font_family, line_height, paragraph_spacing,
			text_indent, content_width, display_mode,
			margin_top, margin_bottom, margin_side,
			preserve_styles, preserve_fonts, justify, hyphenation, theme,
			chapter_title_align, chapter_title_size, chapter_title_spacing,
			font_roles, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			font_size = excluded.font_size,
			font_family = excluded.font_family,
			line_height = excluded.line_height,
			paragraph_spacing = excluded.paragraph_spacing,
			text_indent = excluded.text_indent,
			content_width = excluded.content_width,
			display_mode = excluded.display_mode,
			margin_top = excluded.margin_top,
			margin_bottom = excluded.margin_bottom,
			margin_side = excluded.margin_side,
			preserve_styles = excluded.preserve_styles,
			preserve_fonts = excluded.preserve_fonts,
			justify = excluded.justify,
			hyphenation = excluded.hyphenation,
			theme = excluded.theme,
			chapter_title_align = excluded.chapter_title_align,
			chapter_title_size = excluded.chapter_title_size,
			chapter_title_spacing = excluded.chapter_title_spacing,
			font_roles = excluded.font_roles,
			updated_at = excluded.updated_at
	`, settings.UserID, settings.FontSize, settings.FontFamily, settings.LineHeight, settings.ParagraphSpacing,
		settings.TextIndent, settings.ContentWidth, settings.DisplayMode,
		settings.MarginTop, settings.MarginBottom, settings.MarginSide,
		settings.PreserveStyles, settings.PreserveFonts, settings.Justify, settings.Hyphenation, settings.Theme,
		settings.ChapterTitleAlign, settings.ChapterTitleSize, settings.ChapterTitleSpacing,
		settings.FontRoles, now,
	)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
