import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import '@/design-system/tokens.css';
import '@/design-system/themes.css';
import '@/design-system/utilities.css';
import '@/design-system/components.css';

import { AppProviders } from '@/app/providers';

const root = document.getElementById('root');
if (!root) throw new Error('Missing #root element');

createRoot(root).render(
  <StrictMode>
    <AppProviders />
  </StrictMode>,
);
