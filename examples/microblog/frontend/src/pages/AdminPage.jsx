import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, formatDate } from '../api.js';

export default function AdminPage() {
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [membersOnly, setMembersOnly] = useState(false);
  const [publish, setPublish] = useState(true);

  const [drafts, setDrafts] = useState([]);
  const [error, setError] = useState(null);
  const [notice, setNotice] = useState(null);
  const [busy, setBusy] = useState(false);

  const fileInput = useRef(null);

  useEffect(() => {
    api('/posts/drafts')
      .then((data) => setDrafts(data.posts))
      .catch((err) => setError(err.message));
  }, []);

  async function submit(event) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setNotice(null);

    try {
      const { post } = await api('/posts', {
        method: 'POST',
        body: {
          title,
          body,
          visibility: membersOnly ? 'users' : 'public',
          publish,
        },
      });

      // Attachments are a second request: the post must exist before anything
      // can be attached to it.
      const files = fileInput.current?.files;
      if (files && files.length > 0) {
        const formData = new FormData();
        Array.from(files).forEach((file) => formData.append('files', file));

        await api(`/posts/${post.id}/attachments`, { method: 'POST', formData });
      }

      setNotice(`Published "${post.title}".`);
      setTitle('');
      setBody('');
      setMembersOnly(false);
      if (fileInput.current) fileInput.current.value = '';

      if (!publish) {
        setDrafts((current) => [post, ...current]);
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  async function publishDraft(id) {
    try {
      await api(`/posts/${id}`, { method: 'PATCH', body: { publish: true } });
      setDrafts((current) => current.filter((d) => d.id !== id));
      setNotice('Draft published.');
    } catch (err) {
      setError(err.message);
    }
  }

  async function destroy(id) {
    try {
      await api(`/posts/${id}`, { method: 'DELETE' });
      setDrafts((current) => current.filter((d) => d.id !== id));
      setNotice('Post deleted.');
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <div className="admin">
      <h1>Write a post</h1>

      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}

      <form onSubmit={submit}>
        <label htmlFor="title">Title</label>
        <input
          id="title"
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          maxLength={200}
          required
        />

        <label htmlFor="body">Body</label>
        <textarea
          id="body"
          value={body}
          onChange={(event) => setBody(event.target.value)}
          rows={12}
          placeholder="Blank lines separate paragraphs."
        />

        <label htmlFor="files">Attachments</label>
        <input id="files" type="file" ref={fileInput} multiple />
        <p className="muted small">
          Images and video are shown inline; everything else becomes a download.
        </p>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={membersOnly}
            onChange={(event) => setMembersOnly(event.target.checked)}
          />
          Members only — hide this post and its files from signed-out visitors
        </label>

        <label className="checkbox">
          <input
            type="checkbox"
            checked={publish}
            onChange={(event) => setPublish(event.target.checked)}
          />
          Publish immediately
        </label>

        <button type="submit" disabled={busy}>
          {busy ? 'Saving…' : 'Save post'}
        </button>
      </form>

      {drafts.length > 0 && (
        <section className="drafts">
          <h2>Drafts</h2>
          <ul>
            {drafts.map((draft) => (
              <li key={draft.id}>
                <Link to={`/posts/${draft.slug}`}>{draft.title}</Link>
                <span className="muted"> — {formatDate(draft.createdAt)}</span>
                <button
                  type="button"
                  className="link-button"
                  onClick={() => publishDraft(draft.id)}
                >
                  Publish
                </button>
                <button
                  type="button"
                  className="link-button danger"
                  onClick={() => destroy(draft.id)}
                >
                  Delete
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}
