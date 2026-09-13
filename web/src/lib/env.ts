type DevUser = { uid: string; email: string; displayName?: string; role: "admin" | "member" };

export const env = {
  authMode: (import.meta.env.VITE_AUTH_MODE as "testauth" | "firebase") ?? "testauth",
  apiBaseUrl: (import.meta.env.VITE_API_BASE_URL as string) ?? "/api/v1",
  testAuthUrl: (import.meta.env.VITE_TESTAUTH_URL as string | undefined) ?? "",
  devUsers: parseDevUsers(import.meta.env.VITE_DEV_USERS as string | undefined),
  firebase: {
    apiKey: import.meta.env.VITE_FIREBASE_API_KEY as string | undefined,
    authDomain: import.meta.env.VITE_FIREBASE_AUTH_DOMAIN as string | undefined,
    projectId: import.meta.env.VITE_FIREBASE_PROJECT_ID as string | undefined,
    appId: import.meta.env.VITE_FIREBASE_APP_ID as string | undefined,
  },
};

function parseDevUsers(raw: string | undefined): DevUser[] {
  if (!raw) return [];
  try {
    const arr: unknown = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    return arr as DevUser[];
  } catch {
    return [];
  }
}
