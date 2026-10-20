// ESLint flat config for the Gameplane dashboard.
//
// Focus: catch real bugs (floating promises, missing dependency arrays,
// shadowed variables) without being pedantic about style — Prettier will
// handle formatting. Enable TypeScript and React plugins; keep the rule
// set small enough that CI noise stays low.

import js from "@eslint/js";
import globals from "globals";
import tsParser from "@typescript-eslint/parser";
import tsPlugin from "@typescript-eslint/eslint-plugin";
import reactPlugin from "eslint-plugin-react";
import reactHooksPlugin from "eslint-plugin-react-hooks";
import formatjs from "eslint-plugin-formatjs";

// The formatjs rules cover all dashboard source (spec 019 T058). Test files
// and test helpers are exempt.
const MIGRATED_I18N_PATHS = ["src/**/*.{ts,tsx}"];

export default [
  {
    ignores: ["dist/**", "node_modules/**", "coverage/**"],
  },
  js.configs.recommended,
  {
    files: ["scripts/**/*.{js,mjs}"],
    languageOptions: {
      ecmaVersion: "latest",
      sourceType: "module",
      globals: {
        ...globals.node,
        ...globals.es2022,
      },
    },
    rules: {
      "no-console": "off",
    },
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: "latest",
        sourceType: "module",
        ecmaFeatures: { jsx: true },
        project: "./tsconfig.json",
        tsconfigRootDir: import.meta.dirname,
      },
      globals: {
        ...globals.browser,
        ...globals.es2022,
      },
    },
    plugins: {
      "@typescript-eslint": tsPlugin,
      react: reactPlugin,
      "react-hooks": reactHooksPlugin,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...reactPlugin.configs.recommended.rules,
      ...reactHooksPlugin.configs.recommended.rules,
      "react/react-in-jsx-scope": "off",
      "react/prop-types": "off",
      "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
      "@typescript-eslint/no-explicit-any": "error",
      "@typescript-eslint/no-floating-promises": "error",
      "react-hooks/exhaustive-deps": "warn",
      "no-console": ["warn", { allow: ["warn", "error"] }],
    },
    settings: {
      react: { version: "detect" },
    },
  },
  {
    files: MIGRATED_I18N_PATHS,
    ignores: ["**/*.test.{ts,tsx}", "src/test/**"],
    plugins: { formatjs },
    rules: {
      "formatjs/enforce-description": ["error", "literal"],
      "formatjs/enforce-placeholders": "error",
      "formatjs/no-literal-string-in-jsx": [
        "error",
        {
          props: {
            include: [
              ["*", "aria-label"],
              ["*", "title"],
              ["*", "placeholder"],
              ["*", "alt"],
            ],
          },
        },
      ],
    },
  },
];
