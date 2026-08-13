import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, formatDate } from '../api.js';
import { useAuth } from '../auth.jsx';

export default function Comments({ postId }) {
  const { user } = useAuth();
  const [comments, setComments] = useState([]);
  const [body, setBody] = useState('');
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api(`/posts/${postId}/comments`)
      .then((data) => setComments(data.comments))
      .catch((err) => setError(err.message));
  }, [postId]);

  async function submit(event) {
    event.preventDefault();
    if (!body.trim()) return;

    setBusy(true);
    setError(null);
    try {
      const data = await api(`/posts/${postId}/comments`, {
        method: 'POST',
        body: { body },
      });
      setComments((current) => [...current, data.comment]);
      setBody('');
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  async function remove(id) {
    try {
      await api(`/posts/${postId}/comments/${id}`, { method: 'DELETE' });
      setComments((current) => current.filter((c) => c.id !== id));
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <section className="comments">
      <h3>
        {comments.length} {comments.length === 1 ? 'comment' : 'comments'}
      </h3>

      {error && <p className="error">{error}</p>}

      <ul>
        {comments.map((comment) => (
          <li key={comment.id}>
            <p className="meta">
              <strong>{comment.author.displayName}</strong>
              <time dateTime={comment.createdAt}>{formatDate(comment.createdAt)}</time>

              {/* Authors can delete their own; admins can delete any. */}
              {user && (user.id === comment.author.id || user.isAdmin) && (
                <button
                  type="button"
                  className="link-button danger"
                  onClick={() => remove(comment.id)}
                >
                  Delete
                </button>
              )}
            </p>
            <p>{comment.body}</p>
          </li>
        ))}
      </ul>

      {user ? (
        <form onSubmit={submit} className="comment-form">
          <label htmlFor="comment-body">Add a comment</label>
          <textarea
            id="comment-body"
            value={body}
            onChange={(event) => setBody(event.target.value)}
            rows={4}
            maxLength={4000}
            required
          />
          <button type="submit" disabled={busy}>
            {busy ? 'Posting…' : 'Post comment'}
          </button>
        </form>
      ) : (
        <p className="muted">
          <Link to="/signin">Sign in</Link> to comment.
        </p>
      )}
    </section>
  );
}
