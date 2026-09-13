import { initializeApp, type FirebaseApp } from "firebase/app";
import {
  getAuth,
  GoogleAuthProvider,
  onIdTokenChanged,
  signInWithPopup,
  signOut as fbSignOut,
  type Auth,
  type User,
} from "firebase/auth";
import type { AuthProvider, AuthState } from "./provider";

type Cfg = { apiKey: string; authDomain: string; projectId: string; appId: string };

/**
 * Production auth. onIdTokenChanged is the single source of truth for state —
 * it fires on sign-in, sign-out, and every silent token refresh, so the
 * provider never has to poll or cache a token itself.
 */
export class FirebaseAuthProvider implements AuthProvider {
  private app: FirebaseApp | null = null;
  private auth: Auth | null = null;
  private user: User | null = null;
  private listeners = new Set<(s: AuthState) => void>();
  private state: AuthState = { kind: "initializing" };
  private unsub: (() => void) | null = null;

  constructor(private cfg: Cfg) {}

  async init() {
    if (!this.cfg.apiKey) throw new Error("FirebaseAuthProvider: missing config");
    this.app = initializeApp(this.cfg);
    this.auth = getAuth(this.app);
    this.unsub = onIdTokenChanged(this.auth, (u) => {
      this.user = u;
      if (!u) this.emit({ kind: "signed-out" });
      else
        this.emit({
          kind: "signed-in",
          uid: u.uid,
          email: u.email,
          displayName: u.displayName,
        });
    });
  }

  onChange(cb: (s: AuthState) => void) {
    this.listeners.add(cb);
    cb(this.state);
    return () => {
      this.listeners.delete(cb);
    };
  }

  async signIn() {
    if (!this.auth) throw new Error("FirebaseAuthProvider not initialized");
    await signInWithPopup(this.auth, new GoogleAuthProvider());
  }

  async signOut() {
    if (this.auth) await fbSignOut(this.auth);
  }

  async getIdToken(forceRefresh = false): Promise<string | null> {
    if (!this.user) return null;
    return this.user.getIdToken(forceRefresh);
  }

  /** Stops mirroring Firebase state; used when the app tears the provider down. */
  dispose() {
    this.unsub?.();
    this.unsub = null;
    this.listeners.clear();
  }

  private emit(s: AuthState) {
    this.state = s;
    for (const cb of this.listeners) cb(s);
  }
}
