import { Link, Navigate, Route, Routes } from 'react-router-dom';

import { useAuth } from './auth.jsx';
import AdminPage from './pages/AdminPage.jsx';
import FeedPage from './pages/FeedPage.jsx';
import PostPage from './pages/PostPage.jsx';
import SignInPage from './pages/SignInPage.jsx';

function Header() {
  const { user, signOut } = useAuth();

  return (
    <header className="site-header">
      <Link to="/" className="brand">
        Microblog
      </Link>

      <nav>
        {user?.isAdmin && (
          <Link to="/admin" className="nav-link">
            Write
          </Link>
        )}
        {user ? (
          <>
            <span className="who">{user.displayName}</span>
            <button type="button" className="link-button" onClick={signOut}>
              Sign out
            </button>
          </>
        ) : (
          <Link to="/signin" className="nav-link">
            Sign in
          </Link>
        )}
      </nav>
    </header>
  );
}

/** Guards a route that requires an admin account. */
function RequireAdmin({ children }) {
  const { user, loading } = useAuth();

  if (loading) return <p className="muted">Loading…</p>;
  if (!user) return <Navigate to="/signin" replace />;
  if (!user.isAdmin) return <p className="error">Admins only.</p>;
  return children;
}

export default function App() {
  return (
    <div className="app">
      <Header />

      <main>
        <Routes>
          <Route path="/" element={<FeedPage />} />
          <Route path="/posts/:slug" element={<PostPage />} />
          <Route path="/signin" element={<SignInPage />} />
          <Route
            path="/admin"
            element={
              <RequireAdmin>
                <AdminPage />
              </RequireAdmin>
            }
          />
          <Route path="*" element={<p className="muted">Nothing here.</p>} />
        </Routes>
      </main>

      <footer className="site-footer">
        Deployed with <code>doploy</code>.
      </footer>
    </div>
  );
}
