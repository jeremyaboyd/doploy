import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, formatDate } from '../api.js';
import { useAuth } from '../auth.jsx';

export default function FeedPage() {
  const { user, loading: authLoading } = useAuth();
  const [posts, setPosts] = useState([]);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(true);

  // Reload once auth settles: signing in reveals users-only posts, so the feed
  // is not the same list before and after.
  useEffect(() => {
    if (authLoading) return;

    setLoading(true);
    api('/posts')
      .then((data) => setPosts(data.posts))
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false));
  }, [authLoading, user]);

  if (loading) return <p className="muted">Loading…</p>;
  if (error) return <p className="error">{error}</p>;

  if (posts.length === 0) {
    return (
      <p className="muted">
        Nothing posted yet.{' '}
        {user?.isAdmin ? <Link to="/admin">Write the first post.</Link> : null}
      </p>
    );
  }

  return (
    <ul className="feed">
      {posts.map((post) => (
        <li key={post.id} className="feed-item">
          <h2>
            <Link to={`/posts/${post.slug}`}>{post.title}</Link>
          </h2>

          <p className="meta">
            <time dateTime={post.publishedAt}>{formatDate(post.publishedAt)}</time>
            {post.visibility === 'users' && <span className="badge">Members only</span>}
            <span>
              {post.commentCount} {post.commentCount === 1 ? 'comment' : 'comments'}
            </span>
          </p>

          <p className="excerpt">{post.body.slice(0, 240)}</p>
        </li>
      ))}
    </ul>
  );
}
