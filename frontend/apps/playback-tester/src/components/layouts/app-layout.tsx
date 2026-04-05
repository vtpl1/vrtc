import { Outlet } from 'react-router';
import { useTheme } from '@/hooks/use-theme';
import logoSvg from '@/assets/logo-icon.svg';
import styles from './app-layout.module.css';

export function AppLayout() {
  const { theme, setTheme } = useTheme();

  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <div className={styles.brand}>
          <img src={logoSvg} alt="Videonetics" className={styles.logo} />
          <span className={styles.appName}>Playback Tester</span>
        </div>
        <div className={styles.actions}>
          <select
            className="select"
            value={theme}
            onChange={(e) => setTheme(e.target.value as 'light' | 'dark' | 'system')}
            aria-label="Theme"
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </div>
      </header>
      <main className={styles.main}>
        <Outlet />
      </main>
    </div>
  );
}
