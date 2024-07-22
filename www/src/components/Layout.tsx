import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../features/auth/auth-slice";
import NavBar from "./NavBar";

function Layout() {
  const auth = useAuth();
  const location = useLocation();
  return auth.user ? (
    <>
      <NavBar />
      <Outlet />
    </>
  ) : (
    <Navigate to="login" replace state={{ from: location }} />
  );
}

export default Layout;
