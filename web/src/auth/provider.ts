export type AuthState =
  | { kind: "initializing" }
  | { kind: "signed-out" }
  | {
      kind: "signed-in";
      uid: string;
      email: string | null;
      displayName: string | null;
    };

/**
 * The surface every auth backend must provide. Swapped at build time:
 * TestAuthProvider for the local compose stack, FirebaseAuthProvider for prod.
 */
export interface AuthProvider {
  init(): Promise<void>;
  onChange(cb: (state: AuthState) => void): () => void;
  signIn(...args: unknown[]): Promise<void>;
  signOut(): Promise<void>;
  getIdToken(forceRefresh?: boolean): Promise<string | null>;
}
