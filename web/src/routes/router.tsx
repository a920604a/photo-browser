import { createBrowserRouter, RouterProvider, Navigate } from "react-router-dom";
import { AppShell } from "../components/layout/AppShell";
import { ProtectedShell } from "./protected";
import { Login } from "./login";
import { Forbidden } from "./forbidden";
import { Photos } from "./photos";
import { Timeline } from "./timeline";
import { Albums } from "./albums";
import { AlbumDetail } from "./album-detail";
import { Categories } from "./categories";
import { CategoryDetail } from "./category-detail";
import { Viewer } from "./viewer";
import { Profile } from "./profile";

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  { path: "/forbidden", element: <Forbidden /> },
  {
    element: <ProtectedShell />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/photos" replace /> },
          { path: "/photos", element: <Photos /> },
          { path: "/photos/timeline", element: <Timeline /> },
          { path: "/albums", element: <Albums /> },
          { path: "/albums/:albumId", element: <AlbumDetail /> },
          { path: "/categories", element: <Categories /> },
          { path: "/categories/:categoryId", element: <CategoryDetail /> },
          { path: "/viewer/:photoId", element: <Viewer /> },
          { path: "/profile", element: <Profile /> },
        ],
      },
    ],
  },
]);

export function AppRouter() {
  return <RouterProvider router={router} />;
}
