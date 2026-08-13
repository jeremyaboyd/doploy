import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

import multer from 'multer';

export const UPLOAD_DIR = process.env.UPLOAD_DIR || '/var/lib/microblog/uploads';
const MAX_UPLOAD_MB = Number(process.env.MAX_UPLOAD_MB || 256);

fs.mkdirSync(UPLOAD_DIR, { recursive: true });

/**
 * Classifies an upload for the UI: images render inline, videos get a player,
 * everything else becomes a download link.
 */
export function kindFor(mimeType) {
  if (mimeType.startsWith('image/')) return 'image';
  if (mimeType.startsWith('video/')) return 'video';
  return 'file';
}

/**
 * Stored names are generated, never derived from the uploaded filename.
 *
 * The original name is kept in the database for display and for the download
 * filename, but it never touches the filesystem -- that is what stops a
 * traversal or a collision.
 */
const storage = multer.diskStorage({
  destination(_req, _file, cb) {
    cb(null, UPLOAD_DIR);
  },
  filename(_req, file, cb) {
    const extension = path.extname(file.originalname).slice(0, 16);
    const safeExtension = /^\.[A-Za-z0-9]+$/.test(extension) ? extension.toLowerCase() : '';
    cb(null, `${crypto.randomUUID()}${safeExtension}`);
  },
});

export const upload = multer({
  storage,
  limits: {
    fileSize: MAX_UPLOAD_MB * 1024 * 1024,
    files: 10,
  },
});

/** Absolute path for a stored attachment, guarded against escaping UPLOAD_DIR. */
export function storedPath(storedName) {
  const resolved = path.resolve(UPLOAD_DIR, storedName);
  const root = path.resolve(UPLOAD_DIR);

  if (resolved !== root && !resolved.startsWith(root + path.sep)) {
    throw new Error('attachment path escapes the upload directory');
  }
  return resolved;
}

/** Removes a stored file, ignoring the case where it is already gone. */
export function removeStored(storedName) {
  try {
    fs.unlinkSync(storedPath(storedName));
  } catch (error) {
    if (error.code !== 'ENOENT') {
      console.error(`could not delete ${storedName}: ${error.message}`);
    }
  }
}
