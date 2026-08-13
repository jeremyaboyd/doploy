import express from 'express';

import { attachUser, seedAdmin } from './auth.js';
import { pool, waitForDatabase } from './db.js';
import { attachmentsRouter } from './routes/attachments.js';
import { authRouter } from './routes/auth.js';
import { commentsRouter } from './routes/comments.js';
import { postsRouter } from './routes/posts.js';

const PORT = Number(process.env.PORT || 8080);

const app = express();

// Behind nginx on the same droplet, so the client address arrives in
// X-Forwarded-For.
app.set('trust proxy', 1);
app.use(express.json({ limit: '1mb' }));

// Health checks must not touch the database: this endpoint is what Docker uses
// to decide the container is alive, and a database blip should not restart it.
app.get('/api/healthz', (_req, res) => res.json({ ok: true }));

// Readiness does check the database, for humans debugging a deploy.
app.get('/api/readyz', async (_req, res) => {
  try {
    await pool.query('SELECT 1');
    res.json({ ok: true, database: 'up' });
  } catch (error) {
    res.status(503).json({ ok: false, database: 'down', error: error.message });
  }
});

app.use(attachUser);

app.use('/api/auth', authRouter);
app.use('/api/posts', postsRouter);
app.use('/api/posts/:id/comments', commentsRouter);
app.use('/api/attachments', attachmentsRouter);

app.use((_req, res) => res.status(404).json({ error: 'no such endpoint' }));

// Multer signals an oversized upload with this code; everything else is a bug
// and gets a generic message rather than an internal detail.
app.use((error, _req, res, _next) => {
  if (error?.code === 'LIMIT_FILE_SIZE') {
    return res.status(413).json({ error: 'that file is too large' });
  }
  if (error?.code === 'LIMIT_FILE_COUNT') {
    return res.status(413).json({ error: 'too many files at once' });
  }

  console.error('unhandled error:', error);
  return res.status(500).json({ error: 'something went wrong' });
});

async function main() {
  await waitForDatabase();
  await seedAdmin();

  const server = app.listen(PORT, () => {
    console.log(`microblog api listening on ${PORT}`);
  });

  // Give in-flight requests a chance to finish when the container is replaced.
  const shutdown = (signal) => {
    console.log(`${signal} received, shutting down`);
    server.close(() => pool.end().then(() => process.exit(0)));
    setTimeout(() => process.exit(1), 10_000).unref();
  };

  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));
}

main().catch((error) => {
  console.error('failed to start:', error);
  process.exit(1);
});
