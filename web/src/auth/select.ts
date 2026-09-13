import type { AuthProvider } from "./provider";
import { env } from "../lib/env";

/**
 * Picks the auth backend for this build. The dynamic imports matter: Vite
 * inlines VITE_AUTH_MODE at build time, so a prod (firebase) bundle never pulls
 * in the testauth chunk. Task 22's bundle guard asserts that.
 */
export async function makeAuthProvider(): Promise<AuthProvider> {
  if (env.authMode === "testauth") {
    const mod = await import("./testauth");
    return new mod.TestAuthProvider({ url: env.testAuthUrl, devUsers: env.devUsers });
  }
  if (env.authMode === "firebase") {
    const c = env.firebase;
    if (!c.apiKey || !c.authDomain || !c.projectId || !c.appId) {
      throw new Error("FirebaseAuthProvider: missing VITE_FIREBASE_* env");
    }
    const mod = await import("./firebase");
    return new mod.FirebaseAuthProvider({
      apiKey: c.apiKey,
      authDomain: c.authDomain,
      projectId: c.projectId,
      appId: c.appId,
    });
  }
  throw new Error(`unknown VITE_AUTH_MODE: ${String(env.authMode)}`);
}
