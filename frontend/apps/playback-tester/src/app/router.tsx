import { createBrowserRouter } from 'react-router';
import { AppLayout } from '@/components/layouts/app-layout';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      {
        index: true,
        lazy: () => import('@/features/playback/routes/player'),
      },
    ],
  },
]);
