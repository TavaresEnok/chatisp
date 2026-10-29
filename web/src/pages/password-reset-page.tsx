import { PasswordResetRequestForm } from "@/components/auth/password-reset-forms";
import { AuthScreen } from "@/components/auth/auth-screen";

export function PasswordResetPage() {
  return (
    <AuthScreen title="Recuperar senha">
      <PasswordResetRequestForm />
    </AuthScreen>
  );
}
