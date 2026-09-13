import type { AuthProvider, AuthState } from "./provider";

type DevUser = { uid: string; email: string; displayName?: string; role: "admin" | "member" };
type Opts = { url: string; devUsers: DevUser[]; skewSec?: number };

const TOKEN_KEY = "ta.token";
const UID_KEY = "ta.uid";
const DEFAULT_SKEW = 60;

/**
 * Dev-only auth backed by the compose stack's testauth /mint endpoint. Sign-in
 * is picking one of the seeded dev users; the minted JWT lives in
 * sessionStorage so a page reload keeps the session without a round trip.
 *
 * Never bundled into production — see the auth adapter select in adapter.ts.
 */
export class TestAuthProvider implements AuthProvider {
  private listeners = new Set<(s: AuthState) => void>();
  private state: AuthState = { kind: "initializing" };
  private token: string | null = null;
  private currentUid: string | null = null;
  constructor(private opts: Opts) {}

  async init() {
    const t = sessionStorage.getItem(TOKEN_KEY);
    const uid = sessionStorage.getItem(UID_KEY);
    if (t && uid && !this.isExpired(t)) {
      this.token = t;
      this.currentUid = uid;
      this.emit(this.stateFromUid(uid));
    } else {
      sessionStorage.removeItem(TOKEN_KEY);
      sessionStorage.removeItem(UID_KEY);
      this.emit({ kind: "signed-out" });
    }
  }

  onChange(cb: (s: AuthState) => void) {
    this.listeners.add(cb);
    cb(this.state);
    return () => {
      this.listeners.delete(cb);
    };
  }

  async signIn(uid?: string) {
    const target = uid ?? this.currentUid;
    if (!target) throw new Error("TestAuthProvider.signIn requires uid");
    const user = this.opts.devUsers.find((u) => u.uid === target);
    if (!user) throw new Error(`unknown dev uid: ${target}`);
    this.currentUid = user.uid;
    this.token = await this.mint(user);
    sessionStorage.setItem(TOKEN_KEY, this.token);
    sessionStorage.setItem(UID_KEY, user.uid);
    this.emit(this.stateFromUid(user.uid));
  }

  async signOut() {
    this.token = null;
    this.currentUid = null;
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(UID_KEY);
    this.emit({ kind: "signed-out" });
  }

  async getIdToken(forceRefresh = false): Promise<string | null> {
    if (!this.currentUid) return null;
    if (forceRefresh || !this.token || this.isExpired(this.token)) {
      const user = this.opts.devUsers.find((u) => u.uid === this.currentUid);
      if (!user) return null;
      this.token = await this.mint(user);
      sessionStorage.setItem(TOKEN_KEY, this.token);
    }
    return this.token;
  }

  private async mint(u: DevUser): Promise<string> {
    const url = new URL("mint", this.baseUrl());
    url.searchParams.set("sub", u.uid);
    url.searchParams.set("email", u.email);
    url.searchParams.set("verified", "1");
    const res = await fetch(url.toString());
    if (!res.ok) throw new Error(`testauth mint failed: ${res.status}`);
    return (await res.text()).trim();
  }

  /**
   * The configured URL may be absolute (http://localhost:8090) or a same-origin
   * proxy path (/testauth) — the latter avoids CORS, since testauth serves no
   * CORS headers. Either way the result ends in a slash so "mint" resolves
   * underneath it rather than replacing the last segment.
   */
  private baseUrl(): URL {
    const here = globalThis.location?.href ?? "http://localhost/";
    const base = new URL(this.opts.url || "/", here);
    if (!base.pathname.endsWith("/")) base.pathname += "/";
    return base;
  }

  /** Treats a token expiring within the skew window as already expired. */
  private isExpired(jwt: string): boolean {
    try {
      const [, payload] = jwt.split(".");
      const claims: unknown = JSON.parse(atob(payload.replaceAll("-", "+").replaceAll("_", "/")));
      const exp = (claims as { exp?: unknown }).exp;
      const skew = this.opts.skewSec ?? DEFAULT_SKEW;
      return typeof exp !== "number" || Date.now() / 1000 + skew >= exp;
    } catch {
      return true;
    }
  }

  private stateFromUid(uid: string): AuthState {
    const u = this.opts.devUsers.find((x) => x.uid === uid);
    if (!u) return { kind: "signed-out" };
    return { kind: "signed-in", uid: u.uid, email: u.email, displayName: u.displayName ?? null };
  }

  private emit(s: AuthState) {
    this.state = s;
    for (const cb of this.listeners) cb(s);
  }
}
