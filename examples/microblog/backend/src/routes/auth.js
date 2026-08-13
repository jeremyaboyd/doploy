import express from 'express';

import {
  hashPassword,
  publicUser,
  requireAuth,
  signToken,
  verifyPassword,
} from '../auth.js';
import { query } from '../db.js';

export const authRouter = express.Router();

const EMAIL_PATTERN = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;
const MIN_PASSWORD_LENGTH = 10;

/**
 * Self-service registration. Accounts created here can comment but never write
 * posts -- is_admin is not settable through this endpoint.
 */
authRouter.post('/register', async (req, res, next) => {
  try {
    const email = String(req.body.email || '').trim().toLowerCase();
    const password = String(req.body.password || '');
    const displayName = String(req.body.displayName || '').trim();

    if (!EMAIL_PATTERN.test(email)) {
      return res.status(400).json({ error: 'that does not look like an email address' });
    }
    if (password.length < MIN_PASSWORD_LENGTH) {
      return res
        .status(400)
        .json({ error: `password must be at least ${MIN_PASSWORD_LENGTH} characters` });
    }
    if (displayName.length < 1 || displayName.length > 80) {
      return res.status(400).json({ error: 'display name must be 1-80 characters' });
    }

    const existing = await query('SELECT 1 FROM users WHERE email = $1', [email]);
    if (existing.rows.length > 0) {
      return res.status(409).json({ error: 'that email is already registered' });
    }

    const { rows } = await query(
      `INSERT INTO users (email, password_hash, display_name)
       VALUES ($1, $2, $3)
       RETURNING id, email, display_name, is_admin, created_at`,
      [email, await hashPassword(password), displayName],
    );

    const user = rows[0];
    return res.status(201).json({ token: signToken(user), user: publicUser(user) });
  } catch (error) {
    return next(error);
  }
});

authRouter.post('/login', async (req, res, next) => {
  try {
    const email = String(req.body.email || '').trim().toLowerCase();
    const password = String(req.body.password || '');

    const { rows } = await query(
      `SELECT id, email, password_hash, display_name, is_admin, created_at
       FROM users WHERE email = $1`,
      [email],
    );

    // Same response whether the account is missing or the password is wrong,
    // so this cannot be used to enumerate registered addresses.
    const user = rows[0];
    const ok = user ? await verifyPassword(password, user.password_hash) : false;
    if (!ok) {
      return res.status(401).json({ error: 'wrong email or password' });
    }

    return res.json({ token: signToken(user), user: publicUser(user) });
  } catch (error) {
    return next(error);
  }
});

authRouter.get('/me', requireAuth, (req, res) => {
  res.json({ user: publicUser(req.user) });
});
