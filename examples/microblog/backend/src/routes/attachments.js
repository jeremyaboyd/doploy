import fs from 'node:fs';

import express from 'express';

import { requireAdmin } from '../auth.js';
import { query } from '../db.js';
import { removeStored, storedPath } from '../uploads.js';

export const attachmentsRouter = express.Router();

/**
 * Streams an attachment.
 *
 * Files are served through this handler rather than straight off disk by nginx
 * precisely so the parent post's visibility can be enforced. A 'users' post's
 * attachments are not reachable by URL alone.
 */
attachmentsRouter.get('/:id/download', async (req, res, next) => {
  try {
    const { rows } = await query(
      `SELECT a.*, p.visibility, p.published_at
       FROM attachments a
       JOIN posts p ON p.id = a.post_id
       WHERE a.id = $1`,
      [req.params.id],
    );

    const attachment = rows[0];
    if (!attachment) {
      return res.status(404).json({ error: 'no such attachment' });
    }
    if (!attachment.published_at && !req.user?.is_admin) {
      return res.status(404).json({ error: 'no such attachment' });
    }
    if (attachment.visibility === 'users' && !req.user) {
      return res.status(403).json({ error: 'this file is for signed-in readers' });
    }

    const filePath = storedPath(attachment.stored_name);
    if (!fs.existsSync(filePath)) {
      return res.status(410).json({ error: 'the file is no longer on disk' });
    }

    res.setHeader('Content-Type', attachment.mime_type);
    res.setHeader('Content-Length', attachment.size_bytes);

    // Images and video play inline; anything else downloads under its original
    // name. The filename is quoted and stripped of quotes and newlines so it
    // cannot break out of the header.
    const safeName = attachment.original_name.replace(/["\r\n]/g, '');
    const disposition = attachment.kind === 'file' ? 'attachment' : 'inline';
    res.setHeader('Content-Disposition', `${disposition}; filename="${safeName}"`);

    return fs.createReadStream(filePath).pipe(res);
  } catch (error) {
    return next(error);
  }
});

attachmentsRouter.delete('/:id', requireAdmin, async (req, res, next) => {
  try {
    const { rows } = await query(
      'DELETE FROM attachments WHERE id = $1 RETURNING stored_name',
      [req.params.id],
    );
    if (rows.length === 0) {
      return res.status(404).json({ error: 'no such attachment' });
    }

    removeStored(rows[0].stored_name);
    return res.status(204).end();
  } catch (error) {
    return next(error);
  }
});
