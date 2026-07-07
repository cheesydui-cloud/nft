import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Component, Suspense, lazy } from 'react'
import { UserProvider, useUser, BlurProvider, CopyFmtProvider } from './components/Layout'
import { Loading, ConfirmProvider } from './components/ui'

const Login = lazy(() => import('./pages/Login'))
const Settings = lazy(() => import('./pages/Settings'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const ChangePassword = lazy(() => import('./pages/ChangePassword'))

const NodeList = lazy(() => import('./pages/nodes/List'))
const NodeDetail = lazy(() => import('./pages/nodes/Detail'))
const RulesList = lazy(() => import('./pages/rules/List'))
const RulesDetail = lazy(() => import('./pages/rules/Detail'))
const UserList = lazy(() => import('./pages/users/List'))
const UserDetail = lazy(() => import('./pages/users/Detail'))
const Announcements = lazy(() => import('./pages/Announcements'))
const NodeRepo = lazy(() => import('./pages/NodeRepo'))

const MyDashboard = lazy(() => import('./pages/my/Dashboard'))
const MyRules = lazy(() => import('./pages/my/Rules'))
const MyRuleDetail = lazy(() => import('./pages/my/RuleDetail'))
const MyLandingNodes = lazy(() => import('./pages/my/LandingNodes'))
const Proxies = lazy(() => import('./pages/Proxies'))

// ErrorBoundary: catches render errors in any child component and shows a
// friendly fallback instead of letting the whole page go white.
class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { hasError: false }
  }
  static getDerivedStateFromError() {
    return { hasError: true }
  }
  componentDidCatch(error, info) {
    console.error('ErrorBoundary caught:', error, info)
  }
  render() {
    if (this.state.hasError) {
      return (
        <div className="min-h-screen flex items-center justify-center bg-app">
          <div className="text-center">
            <h1 className="text-xl font-bold text-ink">页面加载出错</h1>
            <p className="mt-2 text-ink-soft">请刷新页面重试，如果问题持续请联系管理员。</p>
            <button onClick={() => window.location.reload()} className="mt-4 btn-primary">刷新页面</button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

function ProtectedRoute({ children }) {
  const { user } = useUser()
  if (user === undefined) return <Loading />
  if (user === null) return <Navigate to="/login" replace />
  return children
}

function AdminRoute({ children }) {
  const { user } = useUser()
  if (user === undefined) return <Loading />
  if (user === null) return <Navigate to="/login" replace />
  if (user.role !== 'admin') return <Navigate to="/my" replace />
  return children
}

function UserRoute({ children }) {
  const { user } = useUser()
  if (user === undefined) return <Loading />
  if (user === null) return <Navigate to="/login" replace />
  if (user.role === 'admin') return <Navigate to="/" replace />
  return children
}

function RootRedirect() {
  const { user } = useUser()
  if (user === undefined) return <Loading />
  if (user === null) return <Navigate to="/login" replace />
  if (user.role !== 'admin') return <Navigate to="/my" replace />
  return <Dashboard />
}

function NotFound() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-app">
      <div className="text-center">
        <h1 className="text-2xl font-bold text-ink">404</h1>
        <p className="mt-2 text-ink-soft">页面不存在</p>
      </div>
    </div>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <UserProvider>
        <ConfirmProvider>
        <BlurProvider>
        <CopyFmtProvider>
        <ErrorBoundary>
        <Suspense fallback={<div className="min-h-screen flex items-center justify-center bg-app"><Loading /></div>}>
        <Routes>
          <Route path="/login" element={<Login />} />

          <Route path="/" element={<RootRedirect />} />

          {/* Admin routes */}
          <Route path="/nodes" element={<AdminRoute><NodeList /></AdminRoute>} />
          <Route path="/nodes/:id" element={<AdminRoute><NodeDetail /></AdminRoute>} />
          <Route path="/rules" element={<AdminRoute><RulesList /></AdminRoute>} />
          <Route path="/rules/:id" element={<AdminRoute><RulesDetail /></AdminRoute>} />
          <Route path="/users" element={<AdminRoute><UserList /></AdminRoute>} />
          <Route path="/users/:id" element={<AdminRoute><UserDetail /></AdminRoute>} />
          <Route path="/settings" element={<AdminRoute><Settings /></AdminRoute>} />
          <Route path="/announcements" element={<AdminRoute><Announcements /></AdminRoute>} />
          <Route path="/node-repo" element={<AdminRoute><NodeRepo /></AdminRoute>} />

          {/* Regular user routes */}
          <Route path="/my" element={<UserRoute><MyDashboard /></UserRoute>} />
          <Route path="/my/rules" element={<UserRoute><MyRules /></UserRoute>} />
          <Route path="/my/rules/:id" element={<UserRoute><MyRuleDetail /></UserRoute>} />
          <Route path="/my/landing" element={<UserRoute><MyLandingNodes /></UserRoute>} />

          {/* Shared routes */}
          <Route path="/proxies" element={<ProtectedRoute><Proxies /></ProtectedRoute>} />
          <Route path="/change-password" element={<ProtectedRoute><ChangePassword /></ProtectedRoute>} />

          <Route path="*" element={<NotFound />} />
        </Routes>
        </Suspense>
        </ErrorBoundary>
        </CopyFmtProvider>
        </BlurProvider>
        </ConfirmProvider>
      </UserProvider>
    </BrowserRouter>
  )
}
