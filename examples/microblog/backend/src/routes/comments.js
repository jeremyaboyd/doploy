import express from 'express';

import { requireAuth } from '../auth.js';
import { query } from '../db.js';

export const commentsRouter = express.Router({ mergeParams: true });

const MAX_COMMENT_LENGTH = 4000;

function serializeComment(row) {
  return {
    id: String(row.id),
    body: row.body,
    createdAt: row.created_at,
    author: { id: String(row.user_id), displayName: row.display_name },
  };
}

/**
 * Loads a post and checks the caller may see it.
 *
 * Comments inherit their post's visibility: a 'users' post must not leak its
 * discussion to anonymous callers who guessed the id.
 */
async function loadVisiblePost(postId, user) {
  const { rows } = await query(
    'SELECT id, visibility, published_at FROM posts WHERE id = $1',
    [postId],
  );
  const post = rows[0];

  if (!post || (!post.published_at && !user?.is_admin)) {
    return { error: { status: 404, message: 'no such post' } };
  }
  if (post.visibility === 'users' && !user) {
    return { error: { status: 403, message: 'this post is for signed-in readers' } };
  }
  return { post };
}

commentsRouter.get('/', async (req, res, next) => {
  try {
    const { post, error } = await loadVisiblePost(req.params.id, req.user);
    if (error) {
      return res.status(error.status).json({ error: error.message });
    }

    const { rows } = await query(
      `SELECT c.*, u.display_name
       FROM comments c
       JOIN users u ON u.id = c.user_id
       WHERE c.post_id = $1
       ORDER BY c.created_at`,
      [post.id],
    );

    return res.json({ comments: rows.map(serializeComment) });
  } catch (err) {
    return next(err);
  }
});

/** Commenting always requires an account, on public and users-only posts alike. */
commentsRouter.post('/', requireAuth, async (req, res, next) => {
  try {
    const body = String(req.body.body || '').trim();
    if (body.length < 1 || body.length > MAX_COMMENT_LENGTH) {
      return res
        .status(400)
        .json({ error: `comment must be 1-${MAX_COMMENT_LENGTH} characters` });
    }

    const { post, error } = await loadVisiblePost(req.params.id, req.user);
    if (error) {
      return res.status(error.status).json({ error: error.message });
    }

    const { rows } = await query(
      `INSERT INTO comments (post_id, user_id, body)
       VALUES ($1, $2, $3)
       RETURNING *`,
      [post.id, req.user.id, body],
    );

    return res.status(201).json({
      comment: serializeComment({ ...rows[0], display_name: req.user.display_name }),
    });
  } catch (err) {
    return next(err);
  }
});

/** A comment can be removed by its author or by any admin. */
commentsRouter.delete('/:commentId', requireAuth, async (req, res, next) => {
  try {
    const { rows } = await query('SELECT user_id FROM comments WHERE id = $1', [
      req.params.commentId,
    ]);
    const comment = rows[0];

    if (!comment) {
      return res.status(404).json({ error: 'no such comment' });
    }
    if (String(comment.user_id) !== String(req.user.id) && !req.user.is_admin) {
      return res.status(403).json({ error: 'that is not your comment' });
    }

    await query('DELETE FROM comments WHERE id = $1', [req.params.commentId]);
    return res.status(204).end();
  } catch (err) {
    return next(err);
  }
});
