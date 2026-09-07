// Shared ESLint flat config for the fleet's hand-written browser scripts.
// Self-contained: Super-Linter's image carries eslint but not @eslint/js or
// the globals package, so the rules and the browser globals are spelled out.
export default [
  {
    files: ["**/*.js", "**/*.mjs"],
    ignores: ["**/vendor/**", "**/_vendor/**", "**/node_modules/**", "**/static/vendor/**"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "script",
      globals: {
        window: "readonly", document: "readonly", navigator: "readonly", console: "readonly",
        setTimeout: "readonly", setInterval: "readonly", clearTimeout: "readonly", clearInterval: "readonly",
        URL: "readonly", ResizeObserver: "readonly", MutationObserver: "readonly", Array: "readonly",
        Math: "readonly", String: "readonly", parseInt: "readonly", Promise: "readonly",
      },
    },
    rules: {
      "no-var": "error",
      "prefer-const": "error",
      "no-undef": "error",
      "no-unused-vars": ["error", { args: "after-used" }],
      "eqeqeq": ["error", "always"],
      "no-implicit-globals": "error",
      "func-names": ["error", "as-needed"],
      "no-alert": "error",
      "prefer-arrow-callback": "error",
    },
  },
];
