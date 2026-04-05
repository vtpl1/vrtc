# Frontend Coding Style Guide

**Last reviewed**: 2026-04-05

**Based on**: React 19.2, Vite 8, TypeScript 5.9+, React Router 7.9+, Redux Toolkit 2.x / RTK Query

**Target**: `frontend/apps/` - all VMS platform frontend applications

This guide is for two things at the same time:

1. building modern UI with current web platform features
2. keeping the codebase maintainable as the product grows

If a recommendation improves visual novelty but makes the code harder to reason about, the maintainable option wins unless there is a measured product need.

---

## Table of Contents

1. [Guiding Principles](#1-guiding-principles)
2. [Project Structure](#2-project-structure)
3. [TypeScript and Linting](#3-typescript-and-linting)
4. [React Patterns](#4-react-patterns)
5. [State and Data Fetching](#5-state-and-data-fetching)
6. [Routing](#6-routing)
7. [Component Design](#7-component-design)
8. [Design System and Styling](#8-design-system-and-styling)
9. [Accessibility](#9-accessibility)
10. [Performance and Browser Platform](#10-performance-and-browser-platform)
11. [Testing and Quality Gates](#11-testing-and-quality-gates)
12. [Vite Configuration](#12-vite-configuration)
13. [Definition of Done](#13-definition-of-done)
14. [Anti-Patterns](#14-anti-patterns)
15. [Sources](#15-sources)

---

## 1. Guiding Principles

1. **Maintainability over cleverness** - prefer obvious code, predictable structure, and shallow abstractions.
2. **Type safety by default** - strict TypeScript, explicit narrowing, minimal assertions.
3. **Accessible by default** - keyboard access, visible focus, reduced motion support, sufficient contrast.
4. **Modern platform first** - use the web platform before adding dependencies.
5. **Tokens before one-off styling** - spacing, color, motion, radius, elevation, and typography must come from shared tokens.
6. **Server state over client state** - use RTK Query for server data; keep client state small and local.
7. **Internationalization by default** - user-facing copy, dates, numbers, and empty states must be localization-ready.
8. **Composition over configuration** - small composable primitives are easier to maintain than giant prop-driven components.
9. **Progressive enhancement** - advanced effects are optional; core workflows must work without them.
10. **Measure before optimizing** - do not add complexity for hypothetical performance issues.

### Maintainability Rules

- Every new shared abstraction must remove real duplication in at least 3 call sites.
- If a helper makes behavior less obvious than inline code, keep the code inline.
- Avoid "universal" components that attempt to solve unrelated product cases.
- Prefer feature ownership over central dumping grounds.
- The easiest file to delete later is usually the best abstraction level today.
- Persist user preferences only when the retained state clearly helps the next session.

---

## 2. Project Structure

### Feature-Based Organization

```text
src/
  app/
    store.ts                  # Redux store and middleware
    router.tsx                # React Router config
    providers.tsx             # Provider composition
    api.ts                    # Base RTK Query createApi
    generated/
      openapi.ts              # RTK Query codegen output from OpenAPI; do not edit manually

  features/
    license/
      api.ts                  # Feature-owned wrappers/re-exports for generated consumers
      types.ts
      routes/
        dashboard.tsx
      components/
        license-card.tsx
      hooks/
      utils/
      index.ts
    cameras/
    dashboard/

  components/
    ui/                       # Reusable primitives only
    layouts/                  # App shell and layout primitives
    feedback/                 # Alert, empty-state, skeleton, toast

  design-system/
    tokens.css                # Global design tokens
    themes.css                # Theme definitions
    utilities.css             # Shared utilities
    components.css            # Shared component class contracts

  i18n/
    index.ts                  # i18n setup and locale helpers
    messages/
      en.json

  hooks/
  lib/                        # Cross-feature libraries, adapters, helpers
  test/
  main.tsx
  vite-env.d.ts
```

### Dependency Direction

- `design-system`, `lib`, `hooks`, `components/ui` may be imported by features
- features may be imported by `app`
- features must not import other features directly
- if two features need the same code, move it into `lib`, `components/ui`, or `design-system`

### Rules

- Each feature exposes a small public API through its own `index.ts`.
- Shared folders must stay curated. If a file is only used by one feature, keep it in that feature.
- Co-locate tests, story files, styles, and helper types with the component or route they support.
- Use kebab-case for file names and PascalCase for component names.
- A file should usually have one primary export.
- Generated API consumers should enter feature code through a feature-owned boundary such as `features/<name>/api.ts` or `features/<name>/index.ts`.

---

## 3. TypeScript and Linting

### tsconfig.json

```json
{
  "compilerOptions": {
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "exactOptionalPropertyTypes": true,
    "verbatimModuleSyntax": true,
    "noUncheckedSideEffectImports": true,
    "module": "esnext",
    "moduleResolution": "bundler",
    "target": "esnext",
    "jsx": "react-jsx",
    "isolatedModules": true,
    "noEmit": true,
    "baseUrl": ".",
    "paths": {
      "@/*": ["./src/*"]
    }
  }
}
```

### Naming

| Element | Convention | Example |
|---|---|---|
| Interface / Type | PascalCase | `LicenseStatus`, `CameraSummary` |
| Props | `{Component}Props` | `DialogProps` |
| Files | kebab-case | `camera-grid.tsx` |
| Hooks | `use` prefix | `use-theme` file, `useTheme` symbol |
| Constants | camelCase or SCREAMING_SNAKE | `featureLabels`, `API_TIMEOUT_MS` |

### Type Rules

- Prefer `interface` for component props and object-shaped extension points.
- Prefer `type` for unions, mapped types, and utility composition.
- Prefer discriminated unions over boolean flag combinations.
- Prefer `satisfies` for config objects.
- Use `unknown` instead of `any`.
- Keep assertions rare and local. If an assertion is unavoidable, document why.

```ts
type AsyncState<T> =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'success'; data: T }
  | { status: 'error'; message: string };

const routes = {
  home: '/',
  cameras: '/cameras',
  settings: '/settings',
} satisfies Record<string, string>;
```

### Imports

```ts
// 1. Framework
import { useEffectEvent, useState } from 'react';
import { NavLink } from 'react-router';

// 2. Third-party
import { skipToken } from '@reduxjs/toolkit/query/react';

// 3. App aliases
import { useGetCameraQuery } from '@/features/cameras/api';
import type { Camera } from '@/features/cameras/types';

// 4. Relative
import { CameraCard } from './camera-card';
```

### Linting

- Use ESLint flat config.
- Enable `typescript-eslint`.
- Enable `eslint-plugin-react-hooks` latest recommended config.
- Treat lint warnings about hooks, effects, and accessibility as design feedback, not noise.
- Do not disable lint rules globally to make code "fit".

---

## 4. React Patterns

### React 19.2

Use React 19 APIs when they reduce code or improve correctness:

- `ref` as a prop instead of `forwardRef`
- direct context providers with `<Context value={...}>`
- `useActionState` for form actions where it simplifies state flow
- `useOptimistic` for local optimistic UI
- `useEffectEvent` to separate effect-only event logic from reactive dependencies

```tsx
import { useEffect, useEffectEvent } from 'react';

interface ConnectionBadgeProps {
  roomId: string;
  theme: 'light' | 'dark';
}

export function ConnectionBadge({ roomId, theme }: ConnectionBadgeProps) {
  const onConnected = useEffectEvent(() => {
    showToast({ message: 'Connected', theme });
  });

  useEffect(() => {
    const connection = connectToRoom(roomId);
    connection.on('connected', onConnected);
    return () => connection.disconnect();
  }, [roomId]);

  return null;
}
```

### React Compiler

React Compiler is stable, but it is still an explicit build choice.

- If the app has React Compiler enabled, prefer plain code and avoid cargo-cult `useMemo`, `useCallback`, and `React.memo`.
- If the app does **not** have the compiler enabled, memoize only where profiling shows a real rendering problem.
- Leave existing memoization in place unless you have verified behavior and performance after removal.

```tsx
interface DashboardProps {
  data: Camera[];
  onSelect(id: string): void;
}

export function Dashboard({ data, onSelect }: DashboardProps) {
  const rows = groupCamerasByZone(data);

  return (
    <section>
      {rows.map((row) => (
        <CameraRow key={row.zoneId} row={row} onSelect={onSelect} />
      ))}
    </section>
  );
}
```

### Component Rules

- Prefer named exports.
- Keep render functions pure.
- Return early for loading, error, unauthorized, and empty states.
- Keep side effects in hooks, not render branches.
- Split components when one file starts mixing data loading, layout orchestration, and detailed presentation.
- Aim for components under 200 lines. Go above that only when splitting would reduce clarity.

---

## 5. State and Data Fetching

### Ownership Rules

- RTK Query owns server data.
- OpenAPI owns the API contract.
- Generated RTK Query endpoint consumers own the default fetch layer for documented backend endpoints.
- Local component state owns transient UI state.
- Redux slices are for cross-tree client state only when local state or context is not enough.
- Do not mirror RTK Query data into component state unless the UI is explicitly editing a draft.
- Persisted user preferences own durable user choices such as theme, density, locale, and table configuration.

### API Contract Rules

- Default to OpenAPI as the source of truth for documented backend endpoints.
- Generate RTK Query endpoint consumers from the OpenAPI schema instead of hand-writing hooks for standard CRUD endpoints.
- Commit generated code when the repo workflow expects it; otherwise make regeneration part of CI or local setup.
- Re-export or wrap generated consumers from the owning feature boundary. Avoid scattering raw imports from `app/generated/openapi` across route and component files.
- Hand-written endpoint definitions are allowed only for:
  - endpoints not present in the schema yet
  - temporary backend mismatches that are being actively corrected
  - frontend-only adapters that wrap generated hooks without replacing them
- Never edit generated files manually. Fix the schema, codegen config, or wrapper code instead.

### Base API

```ts
import { createApi, fetchBaseQuery } from '@reduxjs/toolkit/query/react';
import { authSession } from '@/lib/auth/session';

export const api = createApi({
  reducerPath: 'api',
  baseQuery: fetchBaseQuery({
    baseUrl: '/api/v1',
    prepareHeaders: (headers) => {
      const token = authSession.getAccessToken();
      if (token) headers.set('Authorization', `Bearer ${token}`);
      return headers;
    },
  }),
  tagTypes: ['Camera', 'License', 'Feature', 'User'],
  endpoints: () => ({}),
});
```

### Feature Endpoints

Prefer generated consumers through a feature-owned boundary:

```ts
import {
  useGetCameraByIdQuery,
  usePatchCameraByIdMutation,
} from '@/app/generated/openapi';

export {
  useGetCameraByIdQuery as useGetCameraQuery,
  usePatchCameraByIdMutation as useUpdateCameraMutation,
};
```

Hand-written endpoints should be the exception, not the default:

```ts
import { api } from '@/app/api';
import type { Camera, UpdateCameraRequest } from './types';

const camerasApi = api.injectEndpoints({
  endpoints: (build) => ({
    getCamera: build.query<Camera, string>({
      query: (id) => `cameras/${id}`,
      providesTags: (_result, _error, id) => [{ type: 'Camera', id }],
    }),
    updateCamera: build.mutation<Camera, UpdateCameraRequest>({
      query: ({ id, ...body }) => ({
        url: `cameras/${id}`,
        method: 'PATCH',
        body,
      }),
      invalidatesTags: (_result, _error, { id }) => [{ type: 'Camera', id }],
    }),
  }),
});

export const { useGetCameraQuery, useUpdateCameraMutation } = camerasApi;
```

### Rules

- Use `skipToken` for conditional queries.
- Use optimistic updates only for interactions that users expect to feel instant.
- Keep endpoint names stable and descriptive.
- Prefer one source of truth per entity. Do not fetch the same resource via both route loaders and RTK Query without a clear ownership boundary.
- Prefer generated RTK Query hooks over custom hooks when an OpenAPI-backed consumer already exists.

### User Preference Retention

Retain preferences that improve repeat usability:

- theme
- locale
- density mode
- dismissed onboarding or coach marks
- table view mode, sort, visible columns, and page size
- last-used non-sensitive filters when product behavior benefits from continuity

Rules:

- Use durable storage for stable preferences, such as `localStorage`, only for non-sensitive values.
- Treat persisted preferences as user-owned state and version their shape when needed.
- Do not persist secrets, tokens, or anything security-sensitive in browser storage.
- Do not persist transient state that surprises users when they return.
- Clear or migrate incompatible preference data explicitly.

### Recommended Preference Storage

Default recommendation:

- Use a small typed wrapper around `localStorage` for non-sensitive browser preferences such as theme, locale, density, and table options.
- Keep all persisted preference keys in one module, with versioning and migration helpers.

Optional:

- Use `idb-keyval` if the persisted preference payload becomes too large or structured for comfortable `localStorage` usage.

Use sparingly:

- Use `redux-persist` only when multiple Redux slices genuinely need coordinated rehydration. It should not be the default solution for saving theme or locale.

Selection rule:

- Prefer narrow persistence over whole-store persistence.

---

## 6. Routing

### React Router 7

Use route modules and lazy loading for maintainable route boundaries.

```tsx
import { createBrowserRouter } from 'react-router';
import { AppLayout } from '@/components/layouts/app-layout';

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, lazy: () => import('@/features/dashboard/routes/home') },
      { path: 'cameras', lazy: () => import('@/features/cameras/routes/list') },
      { path: 'cameras/:cameraId', lazy: () => import('@/features/cameras/routes/detail') },
    ],
  },
]);
```

### Route Rules

- Route files own route-level layout, data orchestration, metadata, and error boundaries.
- Presentation details belong in child components, not route modules.
- Prefer generated route types when using React Router typegen.
- Use route-level error boundaries for failures users can recover from.
- Use view transitions only as enhancement, never as a dependency for core navigation.

---

## 7. Component Design

### Composition Over Configuration

```tsx
<Card>
  <CardHeader>
    <CardTitle>Cameras</CardTitle>
    <Badge tone="info">12 online</Badge>
  </CardHeader>
  <CardContent>
    <CameraGrid cameras={cameras} />
  </CardContent>
</Card>
```

Avoid:

```tsx
<Card title="Cameras" badgeText="12 online" badgeTone="info" content={<CameraGrid cameras={cameras} />} />
```

### Shared Component Layers

1. **Primitives**: `Button`, `Input`, `Dialog`, `Badge`
2. **Patterns**: `PageHeader`, `EmptyState`, `FilterBar`, `DataTable`
3. **Feature components**: domain-specific UI built from the first two layers

Do not put business logic into primitives.

### Loading, Empty, and Error States

Every route and data-heavy component must define:

- loading state
- error state
- empty state
- success state

Skeletons should match final layout shape. Do not use generic gray boxes everywhere if the final UI has clear structure.

### Forms

- Use native form semantics first.
- Prefer React 19 form actions when they simplify async submission flow.
- Keep validation close to the form boundary.
- Separate parsing, validation, and submission logic.
- Keep validation and error copy localization-ready.

---

## 8. Design System and Styling

### Visual Standard

Modern UI is not only "dark mode + cards". Shared styling must define:

- typography scale
- spacing scale
- radius scale
- elevation scale
- color tokens
- motion tokens
- layout constraints
- component state tokens
- wide-screen behavior
- narrow laptop behavior

### CSS Architecture

Use CSS layers to keep styles predictable:

```css
@layer reset, tokens, base, utilities, components, overrides;
```

Recommended ownership:

- `tokens.css` in `@layer tokens`
- global element styles in `@layer base`
- utility classes in `@layer utilities`
- reusable component classes in `@layer components`

### Design Tokens

```css
@layer tokens {
  :root,
  [data-theme="light"] {
    --font-sans: "IBM Plex Sans", "Segoe UI", sans-serif;
    --font-mono: "JetBrains Mono", monospace;

    --text-xs: 0.75rem;
    --text-sm: 0.875rem;
    --text-md: 1rem;
    --text-lg: 1.125rem;
    --text-xl: 1.375rem;
    --text-2xl: 1.75rem;

    --leading-tight: 1.2;
    --leading-normal: 1.5;
    --leading-loose: 1.7;

    --space-1: 0.25rem;
    --space-2: 0.5rem;
    --space-3: 0.75rem;
    --space-4: 1rem;
    --space-6: 1.5rem;
    --space-8: 2rem;

    --radius-sm: 0.5rem;
    --radius-md: 0.75rem;
    --radius-lg: 1rem;

    --shadow-sm: 0 1px 2px rgb(15 23 42 / 0.08);
    --shadow-md: 0 10px 30px rgb(15 23 42 / 0.12);

    --page-max-readable: 72rem;
    --page-max-app: 120rem;
    --panel-max: 32rem;

    --color-bg: #f5f7fb;
    --color-surface: #ffffff;
    --color-surface-2: #eef2f8;
    --color-border: #d8e0eb;
    --color-text: #122033;
    --color-text-muted: #5d6b82;
    --color-accent: #0f6cbd;
    --color-success: #188038;
    --color-warning: #b06000;
    --color-danger: #c5221f;

    --motion-fast: 120ms;
    --motion-base: 180ms;
    --motion-slow: 280ms;
    --ease-standard: cubic-bezier(0.2, 0, 0, 1);
  }

  [data-theme="dark"] {
    --color-bg: #0b1220;
    --color-surface: #111b2e;
    --color-surface-2: #18243a;
    --color-border: #28354d;
    --color-text: #ecf3ff;
    --color-text-muted: #97a6c0;
    --color-accent: #66b3ff;
    --color-success: #5ccf7a;
    --color-warning: #ffb14d;
    --color-danger: #ff7d78;
    --shadow-sm: 0 1px 2px rgb(0 0 0 / 0.28);
    --shadow-md: 0 12px 32px rgb(0 0 0 / 0.4);
  }
}
```

### Styling Rules

- All production colors come from tokens, not ad hoc hex values in components.
- Prefer semantic tokens such as `--color-surface`, `--color-text-muted`, `--color-accent`.
- Use CSS Modules or well-scoped global classes. Avoid CSS-in-JS runtime unless there is a hard requirement.
- Use container queries for component-level responsiveness.
- Use CSS Grid and flexbox intentionally. Do not over-nest wrappers to force layout.
- Prefer `gap` over margin choreography.
- Use `clamp()` for fluid typography and spacing where it improves readability.
- Do not assume a single "desktop" layout fits all widths above tablet.

### Layout Across Screen Shapes

Support both ultrawide displays and narrow laptop screens as first-class layouts.

Rules:

- Define page-level max widths intentionally. Do not let dense content stretch indefinitely across ultrawide screens.
- Use multi-column expansion only when it improves scanning or comparison.
- On ultrawide screens, prefer centered content regions, side panels, or bounded grids over full-width text blocks.
- On narrow laptops, protect core workflows from horizontal overflow, collapsed actions, and cramped filter bars.
- Test common desktop productivity widths, not only mobile and a large desktop mock width.
- Promote secondary panels to drawers, tabs, or stacked sections when width becomes constrained.
- Tables and dashboards must have an explicit narrow-laptop behavior, not just "shrink until broken".

Recommended checkpoints:

- narrow laptop: roughly 1280x720 or 1366x768
- standard desktop: roughly 1440x900 or 1536x864
- ultrawide: roughly 2560x1080 or 3440x1440

Preferred patterns:

- dashboards: cap primary content width and add extra columns gradually
- forms: keep readable line length and avoid over-wide single-column forms
- data tables: define sticky columns, column priority, wrapping, or alternate compact views
- app shells: use resizable or collapsible sidebars instead of permanently consuming wide-screen space

### Container Queries

Use container queries for reusable components that need to adapt based on available space rather than viewport width.

```css
.camera-panel {
  container-type: inline-size;
}

@container (width >= 42rem) {
  .camera-panel__body {
    grid-template-columns: 2fr 1fr;
  }
}
```

Combine container queries with page-level layout constraints:

```css
.page-shell {
  width: min(100% - 2rem, 120rem);
  margin-inline: auto;
}

.analytics-grid {
  display: grid;
  gap: var(--space-4);
  grid-template-columns: repeat(12, minmax(0, 1fr));
}
```

### Motion

- Motion must support `prefers-reduced-motion`.
- Use motion to clarify state change, not decorate every interaction.
- Keep durations short and easing consistent.
- Prefer route/page reveal, list stagger, and view transition polish over random hover theatrics.

### Themes

- Every screen must work in both light and dark themes.
- Respect system preference by default.
- Persist explicit user overrides.
- Test contrast and legibility in both themes.
- Do not design dark mode as an inverted afterthought.

### Internationalization

- No user-facing copy inline in reusable components unless it is passed in or resolved through the i18n layer.
- Use translation keys or message descriptors, not ad hoc string concatenation.
- Format dates, times, numbers, and relative times with locale-aware APIs.
- Design for text expansion; do not build layouts that only work in English string lengths.
- Prefer neutral component APIs such as `label`, `title`, and `description` props over hardcoded copy.
- Keep pluralization and interpolation in the i18n system, not in hand-built string templates.

```ts
const cameraCountLabel = intl.formatMessage(
  { id: 'cameras.count', defaultMessage: '{count, plural, one {# camera} other {# cameras}}' },
  { count },
);

const updatedAtLabel = new Intl.DateTimeFormat(locale, {
  dateStyle: 'medium',
  timeStyle: 'short',
}).format(updatedAt);
```

### Recommended I18n Libraries and Tools

Default recommendation:

- Use `react-intl` when the app wants strict message descriptors, ICU messages, and a predictable extraction and verification workflow.
- Use built-in `Intl` APIs for dates, numbers, currencies, lists, and relative time regardless of the main i18n library.

Alternative:

- Use `react-i18next` when the product already uses i18next namespaces, runtime translation loading, or an i18next-based localization workflow across services.

Tooling:

- If using `react-intl`, use `@formatjs/cli` for extraction, verification, and compilation.
- If using `react-i18next`, keep namespaces explicit and avoid hiding keys behind ad hoc helper wrappers.

Selection rule:

- Choose one app-wide i18n approach per application. Do not mix `react-intl` and `react-i18next` in the same app without a migration plan.

---

## 9. Accessibility

Accessibility is part of maintainability. Inaccessible UI usually becomes harder to test, harder to extend, and more fragile.

### Rules

- Start with semantic HTML.
- Every interactive control must be keyboard accessible.
- Use `:focus-visible` and keep focus indicators obvious.
- Support `prefers-reduced-motion`.
- Verify high-contrast / `forced-colors` behavior for critical flows.
- Use ARIA only when native HTML cannot express the behavior.
- Dialogs, menus, tabs, comboboxes, grids, and tree views must follow WAI-ARIA Authoring Practices.

### Focus Style Example

```css
:focus-visible {
  outline: 3px solid var(--color-accent);
  outline-offset: 2px;
}

@media (prefers-reduced-motion: reduce) {
  *,
  *::before,
  *::after {
    animation-duration: 1ms !important;
    animation-iteration-count: 1 !important;
    transition-duration: 1ms !important;
    scroll-behavior: auto !important;
  }
}
```

### Content Rules

- Use real button text, not icon-only controls, unless an accessible name is provided.
- Use headings in order.
- Ensure error messages identify the field and the problem.
- Provide loading feedback for async actions longer than about 300ms.
- Localize accessibility labels, descriptions, and validation messages.

---

## 10. Performance and Browser Platform

### General Rules

- Target modern browsers first. Vite 8 production builds target Baseline Widely Available browsers by default.
- Prefer platform features before libraries when the feature is stable enough for the product support matrix.
- Profile before optimizing.

### Recommended Modern Platform Features

- container queries for component responsiveness
- CSS cascade layers for predictable style ownership
- View Transitions API for navigation polish where supported
- `loading="lazy"` and size-appropriate images
- `content-visibility` for heavy off-screen sections when measured useful

### View Transitions

Use view transitions for route changes and stateful layout polish only when:

- the fallback experience remains correct
- IDs and `view-transition-name` values are stable
- reduced-motion users are respected

### Lists and Tables

- Virtualize large lists only when needed.
- Do not virtualize small lists "just in case".
- Prefer pagination or progressive loading if it simplifies the UX.

---

## 11. Testing and Quality Gates

### Test Layers

1. **Vitest + Testing Library** for component behavior
2. **MSW** for networked UI tests
3. **Storybook** for shared component review and accessibility checks
4. **Playwright** for critical user journeys in a real browser

### Rules

- Shared UI primitives must have stories.
- Route-critical flows must have Playwright coverage.
- Add automated accessibility checks where practical.
- Test both light and dark themes for shared components.
- Add at least one reduced-motion check for motion-heavy features.
- Test at least one non-default locale for shared or high-traffic flows.
- Test retained preferences that materially affect layout or behavior.
- Test narrow-laptop and ultrawide layouts for pages with complex information density.

### Component Test Pattern

```ts
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { EmptyState } from './empty-state';

describe('EmptyState', () => {
  it('renders the title and body copy', () => {
    render(<EmptyState title="No cameras" description="Add a camera to begin." />);

    expect(screen.getByRole('heading', { name: 'No cameras' })).toBeInTheDocument();
    expect(screen.getByText('Add a camera to begin.')).toBeInTheDocument();
  });
});
```

### Playwright Expectations

- smoke test route loading
- form submission success and failure
- keyboard navigation for critical dialogs and menus
- theme toggle persistence
- empty and error state rendering
- key layout behavior at narrow-laptop and ultrawide widths for dashboard-style pages

### Minimum Verification Matrix

Reviewers should be able to point to concrete checks, not general confidence.

- visual/layout changes: verify light theme, dark theme, narrow laptop, and ultrawide
- form changes: verify success, validation, and failure states
- data-fetching changes: verify loading, empty, error, and success states
- preference changes: verify persistence, migration fallback, and reset behavior when relevant
- i18n changes: verify at least one non-default locale and locale-aware formatting
- accessibility-sensitive changes: verify keyboard flow and visible focus

---

## 12. Vite Configuration

### Standard Setup

Vite 8 uses Rolldown internally. `@vitejs/plugin-react` v6 no longer uses Babel by default.

Use the smallest config that matches the app:

```ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  resolve: {
    tsconfigPaths: true,
  },
  server: {
    port: 3000,
    strictPort: true,
  },
  build: {
    target: 'baseline-widely-available',
    sourcemap: true,
  },
});
```

### React Compiler

Only add React Compiler when the app is ready for it and the team is willing to debug compiler-specific issues. Keep the compiler rollout explicit and documented.

### Environment Variables

```ts
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_BASE_URL: string;
  readonly VITE_APP_TITLE: string;
}
```

Rules:

- Prefix client-exposed variables with `VITE_`.
- Never expose secrets to the client bundle.
- Validate environment variables at startup.

### Tooling Notes

- Vite 8 requires Node.js 20.19+ or 22.12+.
- Keep Vite config minimal; move special-case logic into documented plugins or app code.
- Do not keep legacy plugins after the platform makes them unnecessary.
- Keep RTK Query codegen configuration in source control and document the regeneration command next to the config.

---

## 13. Definition of Done

A frontend change is not done until:

- the architecture fits the ownership rules in this guide
- the UI works in light and dark themes
- the affected UI is verified at narrow-laptop and ultrawide widths when layout changes are involved
- loading, empty, error, and success states are handled
- keyboard and focus behavior are verified
- reduced-motion behavior is acceptable
- localization behavior is verified for the affected UI when copy or formatting changed
- retained user preferences are verified for save, reload, and fallback behavior when persistence changed
- tests and manual checks satisfy the minimum verification matrix for the change type
- styling uses tokens instead of one-off values
- new abstractions are justified by real reuse
- generated API consumers are regenerated if the OpenAPI contract changed

---

## 14. Anti-Patterns

| Anti-Pattern | Problem | Preferred Fix |
|---|---|---|
| `any` | removes type safety | use `unknown` and narrow |
| blanket `as` casts | hides type errors | use type guards or better types |
| giant shared components | hard to reason about and extend | split into primitives and patterns |
| copying server data into local state | stale data and sync bugs | read from RTK Query directly |
| one-off spacing and colors in JSX | visual drift | use design tokens |
| viewport-only responsiveness | brittle reusable components | use container queries where needed |
| global CSS without layers | override chaos | use `@layer` ownership |
| effect code that hides dependencies | stale closures and bugs | use `useEffectEvent` only for true effect events |
| unconditional memoization everywhere | extra code to maintain | rely on compiler if enabled, otherwise profile first |
| route modules full of UI markup | poor route readability | move presentation into child components |
| inaccessible custom controls | broken keyboard and assistive tech support | start with semantic HTML |
| animation without reduced-motion fallback | excludes users | support `prefers-reduced-motion` |
| feature-to-feature imports | hidden coupling | move shared code to `lib` or `components/ui` |
| "shared" folders used as dumping grounds | no ownership | keep feature code local until reuse is proven |
| hand-written endpoint consumers for documented APIs | drift from backend contract | generate RTK Query consumers from OpenAPI |
| hardcoded English copy in shared UI | blocks localization | route all user-facing copy through the i18n layer |
| persisting every piece of UI state | surprising returns and stale UX | persist only stable user preferences with product value |
| unbounded full-width desktop layouts | poor scanning on ultrawide screens | cap content width and expand intentionally |
| "responsive" layouts tested only on mobile and one desktop size | hidden breakage on real laptops and ultrawides | verify narrow-laptop and ultrawide behavior explicitly |

---

## 15. Sources

- React 19 release blog: https://react.dev/blog/2024/12/05/react-19
- React 19.2 release blog: https://react.dev/blog/2025/10/01/react-19-2
- React Compiler introduction: https://react.dev/learn/react-compiler/introduction
- React `useEffectEvent`: https://react.dev/reference/react/useEffectEvent
- Vite 8 announcement: https://vite.dev/blog/announcing-vite8
- Vite shared options: https://vite.dev/config/shared-options.html
- React Router route modules: https://reactrouter.com/en/start/framework/route-module
- React Router type safety: https://reactrouter.com/explanation/type-safety
- RTK Query overview: https://redux-toolkit.js.org/rtk-query/overview
- TypeScript 5.9 release notes: https://www.typescriptlang.org/docs/handbook/release-notes/typescript-5-9.html
- MDN container queries: https://developer.mozilla.org/en-US/docs/Web/CSS/Guides/Containment/Container_queries
- MDN View Transition API: https://developer.mozilla.org/en-US/docs/Web/API/View_Transition_API
- MDN `Intl.DateTimeFormat`: https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Intl/DateTimeFormat
- MDN `localStorage`: https://developer.mozilla.org/en-US/docs/Web/API/Window/localStorage
- MDN `prefers-reduced-motion`: https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@media/prefers-reduced-motion
- MDN `forced-colors`: https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@media/forced-colors
- WAI-ARIA Authoring Practices: https://www.w3.org/WAI/ARIA/apg/
- React Intl docs: https://formatjs.github.io/docs/react-intl
- FormatJS CLI: https://formatjs.github.io/docs/tooling/cli/
- react-i18next docs: https://react.i18next.com/
- idb-keyval: https://github.com/jakearchibald/idb-keyval
- redux-persist: https://github.com/rt2zz/redux-persist
- Storybook accessibility testing: https://storybook.js.org/docs/9/writing-tests/accessibility-testing
- Playwright docs: https://playwright.dev/
