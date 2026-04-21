import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuth, AuthProvider } from '@/hooks/useAuth'
import { AppLayout } from '@/components/layout/AppLayout'
import LoginPage from '@/pages/LoginPage'
import ManualReceiptPage from '@/pages/ManualReceiptPage'
import SetupPage from '@/pages/SetupPage'
import JoinPage from '@/pages/JoinPage'
import DashboardPage from '@/pages/DashboardPage'
import ScanPage from '@/pages/ScanPage'
import ReceiptReviewPage from '@/pages/ReceiptReviewPage'
import ReceiptsPage from '@/pages/ReceiptsPage'
import ProductsPage from '@/pages/ProductsPage'
import ProductDetailPage from '@/pages/ProductDetailPage'
import RulesPage from '@/pages/RulesPage'
import ListsIndexPage from '@/pages/ListsIndexPage'
import ShoppingListPage from '@/pages/ShoppingListPage'
import ImportPage from '@/pages/ImportPage'
import SpreadsheetCommitResult from '@/pages/import/spreadsheet/SpreadsheetCommitResult'
import SettingsPage from '@/pages/SettingsPage'
import AnalyticsPage from '@/pages/AnalyticsPage'
import StoreViewPage from '@/pages/StoreViewPage'
import ProductGroupPage from '@/pages/ProductGroupPage'
import ReviewPage from '@/pages/ReviewPage'
import type { ReactNode } from 'react'

function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated, needsSetup, isLoading } = useAuth()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-body text-neutral-400">Loading...</p>
      </div>
    )
  }

  if (needsSetup) {
    return <Navigate to="/setup" replace />
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  return <>{children}</>
}

function PublicRoute({ children }: { children: ReactNode }) {
  const { isAuthenticated, needsSetup, isLoading } = useAuth()

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <p className="text-body text-neutral-400">Loading...</p>
      </div>
    )
  }

  // If setup is needed, only allow the setup page
  if (needsSetup) {
    return <>{children}</>
  }

  // If already authenticated, redirect to dashboard
  if (isAuthenticated) {
    return <Navigate to="/" replace />
  }

  return <>{children}</>
}

function AppRoutes() {
  return (
    <Routes>
      {/* Public routes */}
      <Route
        path="/login"
        element={
          <PublicRoute>
            <LoginPage />
          </PublicRoute>
        }
      />
      <Route
        path="/setup"
        element={
          <PublicRoute>
            <SetupPage />
          </PublicRoute>
        }
      />
      <Route
        path="/join/:token"
        element={
          <PublicRoute>
            <JoinPage />
          </PublicRoute>
        }
      />

      {/* Protected routes with layout */}
      <Route
        element={
          <ProtectedRoute>
            <AppLayout />
          </ProtectedRoute>
        }
      >
        <Route index element={<DashboardPage />} />
        {/* Placeholder routes — pages will be implemented in later layers */}
        <Route path="stores/:id" element={<StoreViewPage />} />
        <Route path="analytics" element={<AnalyticsPage />} />
        <Route path="products" element={<ProductsPage />} />
        <Route path="products/:id" element={<ProductDetailPage />} />
        <Route path="product-groups/:id" element={<ProductGroupPage />} />
        <Route path="lists" element={<ListsIndexPage />} />
        <Route path="lists/:id" element={<ShoppingListPage />} />
        <Route path="rules" element={<RulesPage />} />
        <Route path="receipts" element={<ReceiptsPage />} />
        <Route path="receipts/new" element={<ManualReceiptPage />} />
        <Route path="receipts/:id" element={<ReceiptReviewPage />} />
        <Route path="import" element={<ImportPage />} />
        <Route path="import/spreadsheet/result" element={<SpreadsheetCommitResult />} />
        <Route path="conversions" element={<Navigate to="/settings?tab=conversions" replace />} />
        <Route path="settings" element={<SettingsPage />} />
        <Route path="scan" element={<ScanPage />} />
        <Route path="review" element={<ReviewPage />} />
      </Route>

      {/* Catch-all */}
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}

function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <AppRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}

export default App
