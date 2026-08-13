import { formatBytes } from '../api.js';

/**
 * Renders a post's attachments by kind: images inline, video in a player, and
 * anything else as a download link.
 *
 * Every URL points at the API, which re-checks the parent post's visibility on
 * each request. Nothing here is served straight off disk.
 */
export default function Attachments({ attachments }) {
  if (!attachments || attachments.length === 0) {
    return null;
  }

  const images = attachments.filter((a) => a.kind === 'image');
  const videos = attachments.filter((a) => a.kind === 'video');
  const files = attachments.filter((a) => a.kind === 'file');

  return (
    <div className="attachments">
      {images.length > 0 && (
        <div className="gallery">
          {images.map((image) => (
            <figure key={image.id}>
              <img src={image.url} alt={image.name} loading="lazy" />
              <figcaption>{image.name}</figcaption>
            </figure>
          ))}
        </div>
      )}

      {videos.map((video) => (
        <figure key={video.id} className="video">
          {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
          <video controls preload="metadata" src={video.url} />
          <figcaption>{video.name}</figcaption>
        </figure>
      ))}

      {files.length > 0 && (
        <ul className="file-list">
          {files.map((file) => (
            <li key={file.id}>
              <a href={file.url} download>
                {file.name}
              </a>
              <span className="muted"> — {formatBytes(file.sizeBytes)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
