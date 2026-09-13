import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      minHeight: { touch: "44px" },
      minWidth: { touch: "44px" },
    },
  },
  plugins: [],
} satisfies Config;
