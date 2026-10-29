import { AuthScreen } from "@/components/auth/auth-screen";
import { LoginForm } from "@/components/auth/login-form";

export function LoginPage() {
  return (
    <AuthScreen title="Entrar">
      <LoginForm />
    </AuthScreen>
  );
}
