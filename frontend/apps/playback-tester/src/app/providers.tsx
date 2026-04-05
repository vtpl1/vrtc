import { Provider } from 'react-redux';
import { RouterProvider } from 'react-router';
import { store } from './store';
import { router } from './router';

export function AppProviders() {
  return (
    <Provider store={store}>
      <RouterProvider router={router} />
    </Provider>
  );
}
