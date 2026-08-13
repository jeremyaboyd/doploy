import crypto from 'node:crypto';

import express from 'express';

import { requireAdmin } from '../auth.js';
import { query, transaction } from '../db.js';
import { kindFor, removeStored, upload } from '../uploads.js';

export const postsRouter = express.Router();

/**
 * Whether the caller may see a post.
 *
 * 'users' posts, and everything attached to them, are hidden from anonymous
 * visitors. This is the single place that rule is expressed; every handler
 * defers to it.
 */
function canSee(post, user) {
  return post.visibility === 'public' || Boolean(user);
}

function slugify(title) {
  const base = title
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60);

  // A short random suffix keeps slugs unique without a retry loop, and stops
  // two posts with the same title from colliding.
  const suffix = crypto.randomBytes(3).toString('hex');
  return base ? `${base}-${suffix}` : suffix;
}

function serializePost(row, attachments = []) {
  return {
    id: String(row.id),
    slug: row.slug,
    title: row.title,
    body: row.body,
    visibility: row.visibility,
    publishedAt: row.published_at,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
    author: { id: String(row.author_id), displayName: row.author_name },
    commentCount: Number(row.comment_count ?? 0),
    attachments: attachments.map(serializeAttachment),
  };
}

function serializeAttachment(row) {
  return {
    id: String(row.id),
    kind: row.kind,
    name: row.original_name,
    mimeType: row.mime_type,
    sizeBytes: Number(row.size_bytes),
    url: `/api/attachments/${row.id}/download`,
  };
}

/** Lists posts the caller may see, newest first. */
postsRouter.get('/', async (req, res, next) => {
  try {
    const visibleToAnonymous = !req.user;

    const { rows } = await query(
      `SELECT p.*, u.display_name AS author_name,
              (SELECT count(*) FROM comments c WHERE c.post_id = p.id) AS comment_count
       FROM posts p
       JOIN users u ON u.id = p.author_id
       WHERE p.published_at IS NOT NULL
         AND ($1::boolean IS FALSE OR p.visibility = 'public')
       ORDER BY p.published_at DESC
       LIMIT 100`,
      [visibleToAnonymous],
    );

    res.json({ posts: rows.map((row) => serializePost(row)) });
  } catch (error) {
    next(error);
  }
});

/** Admin view: every post, including unpublished drafts. */
postsRouter.get('/drafts', requireAdmin, async (_req, res, next) => {
  try {
    const { rows } = await query(
      `SELECT p.*, u.display_name AS author_name, 0 AS comment_count
       FROM posts p
       JOIN users u ON u.id = p.author_id
       WHERE p.published_at IS NULL
       ORDER BY p.created_at DESC`,
    );
    res.json({ posts: rows.map((row) => serializePost(row)) });
  } catch (error) {
    next(error);
  }
});

postsRouter.get('/:slug', async (req, res, next) => {
  try {
    const { rows } = await query(
      `SELECT p.*, u.display_name AS author_name,
              (SELECT count(*) FROM comments c WHERE c.post_id = p.id) AS comment_count
       FROM posts p
       JOIN users u ON u.id = p.author_id
       WHERE p.slug = $1`,
      [req.params.slug],
    );

    const post = rows[0];
    if (!post) {
      return res.status(404).json({ error: 'no such post' });
    }
    if (!post.published_at && !req.user?.is_admin) {
      return res.status(404).json({ error: 'no such post' });
    }
    if (!canSee(post, req.user)) {
      return res.status(403).json({ error: 'this post is for signed-in readers' });
    }

    const attachments = await query(
      'SELECT * FROM attachments WHERE post_id = $1 ORDER BY created_at',
      [post.id],
    );

    return res.json({ post: serializePost(post, attachments.rows) });
  } catch (error) {
    return next(error);
  }
});

postsRouter.post('/', requireAdmin, async (req, res, next) => {
  try {
    const title = String(req.body.title || '').trim();
    const body = String(req.body.body || '');
    const visibility = req.body.visibility === 'users' ? 'users' : 'public';
    const publish = req.body.publish !== false;

    if (title.length < 1 || title.length > 200) {
      return res.status(400).json({ error: 'title must be 1-200 characters' });
    }

    const { rows } = await query(
      `INSERT INTO posts (slug, title, body, visibility, author_id, published_at)
       VALUES ($1, $2, $3, $4, $5, CASE WHEN $6 THEN now() ELSE NULL END)
       RETURNING *`,
      [slugify(title), title, body, visibility, req.user.id, publish],
    );

    const post = { ...rows[0], author_name: req.user.display_name };
    return res.status(201).json({ post: serializePost(post) });
  } catch (error) {
    return next(error);
  }
});

postsRouter.patch('/:id', requireAdmin, async (req, res, next) => {
  try {
    const fields = [];
    const values = [];

    const push = (sql, value) => {
      values.push(value);
      fields.push(`${sql} = $${values.length}`);
    };

    if (req.body.title !== undefined) push('title', String(req.body.title).trim());
    if (req.body.body !== undefined) push('body', String(req.body.body));
    if (req.body.visibility !== undefined) {
      push('visibility', req.body.visibility === 'users' ? 'users' : 'public');
    }
    if (req.body.publish !== undefined) {
      // Publishing stamps the time; unpublishing clears it and hides the post.
      fields.push(`published_at = ${req.body.publish ? 'now()' : 'NULL'}`);
    }

    if (fields.length === 0) {
      return res.status(400).json({ error: 'nothing to update' });
    }

    fields.push('updated_at = now()');
    values.push(req.params.id);

    const { rows } = await query(
      `UPDATE posts SET ${fields.join(', ')} WHERE id = $${values.length} RETURNING *`,
      values,
    );
    if (rows.length === 0) {
      return res.status(404).json({ error: 'no such post' });
    }

    const post = { ...rows[0], author_name: req.user.display_name };
    return res.json({ post: serializePost(post) });
  } catch (error) {
    return next(error);
  }
});

postsRouter.delete('/:id', requireAdmin, async (req, res, next) => {
  try {
    // Read the attachment rows before the cascade removes them, so the files on
    // the volume can be cleaned up too.
    const stored = await query('SELECT stored_name FROM attachments WHERE post_id = $1', [
      req.params.id,
    ]);

    const { rowCount } = await query('DELETE FROM posts WHERE id = $1', [req.params.id]);
    if (rowCount === 0) {
      return res.status(404).json({ error: 'no such post' });
    }

    stored.rows.forEach((row) => removeStored(row.stored_name));
    return res.status(204).end();
  } catch (error) {
    return next(error);
  }
});

/** Attaches one or more uploaded files to a post. */
postsRouter.post(
  '/:id/attachments',
  requireAdmin,
  upload.array('files', 10),
  async (req, res, next) => {
    try {
      const files = req.files || [];
      if (files.length === 0) {
        return res.status(400).json({ error: 'no files were uploaded' });
      }

      const post = await query('SELECT id FROM posts WHERE id = $1', [req.params.id]);
      if (post.rows.length === 0) {
        files.forEach((file) => removeStored(file.filename));
        return res.status(404).json({ error: 'no such post' });
      }

      const created = await transaction(async (client) => {
        const inserted = [];
        for (const file of files) {
          const { rows } = await client.query(
            `INSERT INTO attachments
               (post_id, kind, original_name, stored_name, mime_type, size_bytes)
             VALUES ($1, $2, $3, $4, $5, $6)
             RETURNING *`,
            [
              req.params.id,
              kindFor(file.mimetype),
              file.originalname,
              file.filename,
              file.mimetype,
              file.size,
            ],
          );
          inserted.push(rows[0]);
        }
        return inserted;
      });

      return res.status(201).json({ attachments: created.map(serializeAttachment) });
    } catch (error) {
      // The bytes are already on disk; drop them so a failed insert does not
      // leave orphans on the volume.
      (req.files || []).forEach((file) => removeStored(file.filename));
      return next(error);
    }
  },
);
