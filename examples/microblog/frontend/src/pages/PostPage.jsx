import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';

import { api, formatDate } from '../api.js';
import { useAuth } from '../auth.jsx';
import Attachments from '../components/Attachments.jsx';
import Comments from '../components/Comments.jsx';

export default function PostPage() {
  const { slug } = useParams();
  const { user, loading: authLoading } = useAuth();

  const [post, setPost] = useState(null);
  const [error, setError] = useState(null);
  const [status, setStatus] = useState(0);

  useEffect(() => {
    if (authLoading) return;

    setError(null);
    api(`/posts/${slug}`)
      .then((data) => setPost(data.post))
      .catch((err) => {
        setError(err.message);
        setStatus(err.status);
      });
  }, [slug, authLoading, user]);

  if (error) {
    return (
      <div>
        <p className="error">{error}</p>
        {status === 403 && (
          <p className="muted">
            <Link to="/signin">Sign in</Link> to read members-only posts.
          </p>
        )}
      </div>
    );
  }

  if (!post) return <p className="muted">Loading…</p>;

  return (
    <article className="post">
      <h1>{post.title}</h1>

      <p className="meta">
        <span>{post.author.displayName}</span>
        <time dateTime={post.publishedAt}>{formatDate(post.publishedAt)}</time>
        {post.visibility === 'users' && <span className="badge">Members only</span>}
      </p>

      {/* Rendered as plain text, deliberately. Treating post bodies as HTML
          would make the admin account an XSS vector against every reader. */}
      <div className="body">
        {post.body.split('\n\n').map((paragraph, index) => (
          // eslint-disable-next-line react/no-array-index-key
          <p key={index}>{paragraph}</p>
        ))}
      </div>

      <Attachments attachments={post.attachments} />

      <Comments postId={post.id} />
    </article>
  );
}
