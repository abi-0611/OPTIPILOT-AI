import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from '@/components/Layout'
import SLOOverview from '@/pages/SLOOverview'
import FairnessDashboard from '@/pages/FairnessDashboard'
import DecisionExplorer from '@/pages/DecisionExplorer'
import WhatIfTool from '@/pages/WhatIfTool'

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/slo" replace />} />
          <Route path="slo" element={<SLOOverview />} />
          <Route path="fairness" element={<FairnessDashboard />} />
          <Route path="decisions" element={<DecisionExplorer />} />
          <Route path="whatif" element={<WhatIfTool />} />
          <Route path="*" element={<Navigate to="/slo" replace />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App