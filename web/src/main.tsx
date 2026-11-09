import '../../design/components/bundle.css';
import '../../design/tokens.css';
import './styles/app.css';
import './styles/pages.css';
import './ds';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { queryClient } from './api/hooks';
import { App } from './App';
import { followColorScheme } from './lib/media';

followColorScheme();

createRoot(document.getElementById('root') as HTMLElement).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
