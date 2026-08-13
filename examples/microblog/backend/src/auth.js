import bcrypt from 'bcryptjs';
import jwt from 'jsonwebtoken';

import { query } from './db.js';

const JWT_SECRET = process.env.JWT_SECRET;
const TOKEN_TTL = '7d';

if (!JWT_SECRET) {
  throw new Error('JWT_SECRET is required');
}

export function signToken(user) {
  return jwt.sign(
    { sub: String(user.id), email: user.email, admin: user.is_admin },
    JWT_SECRET,
    { expiresIn: TOKEN_TTL },
  );
}

export function hashPassword(plain) {
  return bcrypt.hash(plain, 12);
}

export function verifyPassword(plain, hash) {
  return bcrypt.compare(plain, hash);
}

export function publicUser(row) {
  return {
    id: String(row.id),
    email: row.email,
    displayName: row.display_name,
    isAdmin: row.is_admin,
    createdAt: row.created_at,
  };
}

/**
 * Reads the bearer token, if any, and attaches req.user.
 *
 * This never rejects. Anonymous requests are legitimate for public posts, so
 * enforcement belongs in requireAuth / requireAdmin, not here.
 */
export async function attachUser(req, _res, next) {
  const header = req.get('authorization') || '';
  const [scheme, token] = header.split(' ');

  if (scheme !== 'Bearer' || !token) {
    return next();
  }

  try {
    const payload = jwt.verify(token, JWT_SECRET);
    const { rows } = await query(
      'SELECT id, email, display_name, is_admin, created_at FROM users WHERE id = $1',
      [payload.sub],
    );
    if (rows.length > 0) {
      req.user = rows[0];
    }
  } catch {
    // An expired or forged token is treated as anonymous rather than an error,
    // so a stale tab still sees public content.
  }
  return next();
}

export function requireAuth(req, res, next) {
  if (!req.user) {
    return res.status(401).json({ error: 'sign in to do that' });
  }
  return next();
}

export function requireAdmin(req, res, next) {
  if (!req.user) {
    return res.status(401).json({ error: 'sign in to do that' });
  }
  if (!req.user.is_admin) {
    return res.status(403).json({ error: 'admins only' });
  }
  return next();
}

/**
 * Creates the admin account described by the environment, once.
 *
 * An existing account is left completely alone: re-running a deploy must not
 * reset a password that has since been changed.
 */
export async function seedAdmin() {
  const email = process.env.ADMIN_EMAIL;
  const password = process.env.ADMIN_PASSWORD;
  const displayName = process.env.ADMIN_DISPLAY_NAME || 'Admin';

  if (!email || !password) {
    console.log('ADMIN_EMAIL/ADMIN_PASSWORD not set, skipping admin seed');
    return;
  }

  const existing = await query('SELECT id FROM users WHERE email = $1', [email]);
  if (existing.rows.length > 0) {
    console.log(`admin ${email} already exists`);
    return;
  }

  await query(
    `INSERT INTO users (email, password_hash, display_name, is_admin)
     VALUES ($1, $2, $3, TRUE)`,
    [email, await hashPassword(password), displayName],
  );
  console.log(`seeded admin ${email}`);
}
