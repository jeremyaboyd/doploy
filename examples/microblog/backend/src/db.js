import pg from 'pg';

const { Pool } = pg;

if (!process.env.DATABASE_URL) {
  throw new Error('DATABASE_URL is required');
}

export const pool = new Pool({
  connectionString: process.env.DATABASE_URL,
  max: 10,
  idleTimeoutMillis: 30_000,
  connectionTimeoutMillis: 10_000,
});

export function query(text, params) {
  return pool.query(text, params);
}

/**
 * Waits for Postgres to accept connections.
 *
 * The database lives on a different droplet that may still be finishing its
 * setup when this container starts. doploy orders the droplets so db is done
 * first, but a restart of just this container should not need that guarantee,
 * so retry rather than crash-loop.
 */
export async function waitForDatabase({ attempts = 30, delayMs = 2000 } = {}) {
  let lastError;

  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      await pool.query('SELECT 1');
      return;
    } catch (error) {
      lastError = error;
      console.log(
        `waiting for postgres (attempt ${attempt}/${attempts}): ${error.message}`,
      );
      await new Promise((resolve) => setTimeout(resolve, delayMs));
    }
  }

  throw new Error(`database never became reachable: ${lastError?.message}`);
}

/** Runs a function inside a transaction, rolling back on any error. */
export async function transaction(fn) {
  const client = await pool.connect();
  try {
    await client.query('BEGIN');
    const result = await fn(client);
    await client.query('COMMIT');
    return result;
  } catch (error) {
    await client.query('ROLLBACK');
    throw error;
  } finally {
    client.release();
  }
}
