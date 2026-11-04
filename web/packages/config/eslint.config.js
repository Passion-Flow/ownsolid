// ESLint rules for the whole web workspace. Run from web/: `pnpm lint`.
import js from '@eslint/js';
import jsxA11y from 'eslint-plugin-jsx-a11y-x';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';
import tseslint from 'typescript-eslint';

// Product text lives only in locales/ catalogs (AGENTS.md section 7). These selectors
// reject user-visible literals in JSX so every string goes through the translator.
const visibleLiterals = [
  {
    selector: 'JSXText[value=/[A-Za-z]/]',
    message: 'Put user-visible text in the locale catalogs and render it with t().',
  },
  {
    selector:
      'JSXAttribute[name.name=/^(aria-label|aria-description|title|placeholder|alt)$/] > Literal',
    message: 'Put user-visible text in the locale catalogs and render it with t().',
  },
];

export default tseslint.config(
  {
    ignores: [
      '**/dist/',
      '**/node_modules/',
      '**/gen/',
      '**/test-results/',
      '**/playwright-report/',
    ],
  },
  js.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    extends: [...tseslint.configs.strictTypeChecked, ...tseslint.configs.stylisticTypeChecked],
    languageOptions: {
      parserOptions: { projectService: true },
      globals: globals.browser,
    },
    rules: {
      '@typescript-eslint/consistent-type-imports': 'error',
      '@typescript-eslint/restrict-template-expressions': ['error', { allowNumber: true }],
    },
  },
  {
    files: ['**/*.tsx'],
    extends: [reactHooks.configs.flat['recommended-latest'], jsxA11y.configs.strict],
    rules: { 'no-restricted-syntax': ['error', ...visibleLiterals] },
  },
  {
    files: ['**/*.test.{ts,tsx}', '**/e2e/**'],
    rules: { 'no-restricted-syntax': 'off' },
  },
  {
    files: ['**/*.js', '**/vite.config.ts', '**/playwright.config.ts'],
    languageOptions: { globals: globals.node },
  },
  {
    files: ['**/*.js'],
    extends: [tseslint.configs.disableTypeChecked],
  },
);
