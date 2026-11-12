import babelParser from "@babel/eslint-parser";
import js from "@eslint/js";
import globals from "globals";
import reactHooksPlugin from "eslint-plugin-react-hooks";
import formatjs from "eslint-plugin-formatjs";

// The formatjs rules cover all dashboard source (spec 019 T058). Test files
// and test helpers are exempt.
const MIGRATED_I18N_PATHS = ["src/**/*.{ts,tsx}"];
const tsSyntax = ["typescript", "decorators", "decoratorAutoAccessors", "deferredImportEvaluation"];

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
      parser: babelParser,
      parserOptions: {
        requireConfigFile: false,
        babelOptions: {
          parserOpts: { plugins: tsSyntax },
        },
      },
      globals: {
        ...globals.browser,
        ...globals.es2022,
      },
    },
    plugins: {
      "react-hooks": reactHooksPlugin,
    },
    rules: {
      ...reactHooksPlugin.configs.recommended.rules,
      // TypeScript and Oxlint check names in both type and value positions.
      "no-undef": "off",
      "no-redeclare": "off",
      "no-unused-vars": "off",
      "no-console": ["warn", { allow: ["warn", "error"] }],
    },
  },
  {
    files: ["src/**/*.tsx"],
    languageOptions: {
      parserOptions: {
        babelOptions: { parserOpts: { plugins: [...tsSyntax, "jsx"] } },
      },
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
