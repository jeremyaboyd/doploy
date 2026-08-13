import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { useAuth } from '../auth.jsx';

export default function SignInPage() {
  const { signIn, register } = useAuth();
  const navigate = useNavigate();

  const [mode, setMode] = useState('signin');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  const registering = mode === 'register';

  async function submit(event) {
    event.preventDefault();
    setBusy(true);
    setError(null);

    try {
      if (registering) {
        await register(email, password, displayName);
      } else {
        await signIn(email, password);
      }
      navigate('/');
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-page">
      <h1>{registering ? 'Create an account' : 'Sign in'}</h1>
      <p className="muted">
        Accounts are for commenting and reading members-only posts. Only the
        admin can publish.
      </p>

      {error && <p className="error">{error}</p>}

      <form onSubmit={submit}>
        {registering && (
          <>
            <label htmlFor="displayName">Display name</label>
            <input
              id="displayName"
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
              maxLength={80}
              required
            />
          </>
        )}

        <label htmlFor="email">Email</label>
        <input
          id="email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          required
        />

        <label htmlFor="password">Password</label>
        <input
          id="password"
          type="password"
          autoComplete={registering ? 'new-password' : 'current-password'}
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          minLength={registering ? 10 : undefined}
          required
        />

        <button type="submit" disabled={busy}>
          {busy ? 'Working…' : registering ? 'Create account' : 'Sign in'}
        </button>
      </form>

      <button
        type="button"
        className="link-button"
        onClick={() => {
          setMode(registering ? 'signin' : 'register');
          setError(null);
        }}
      >
        {registering ? 'I already have an account' : 'I need an account'}
      </button>
    </div>
  );
}
