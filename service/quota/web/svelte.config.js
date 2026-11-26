import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

// The charts, the latency curve and the node editor are focusable groups
// worked with the pointer and arrow keys, which these checks don't know.
const allowed = new Set([
  "a11y_no_noninteractive_tabindex",
  "a11y_no_noninteractive_element_interactions",
  "a11y_no_static_element_interactions",
  "a11y_no_noninteractive_element_to_interactive_role",
]);

export default {
  preprocess: vitePreprocess(),
  compilerOptions: {
    warningFilter: (warning) => !allowed.has(warning.code),
  },
};
