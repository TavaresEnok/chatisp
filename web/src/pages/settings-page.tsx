import { AreaPage } from "@/components/layout/area-page";
import { LocalDisplayNameForm } from "@/components/settings/local-display-name-form";
import { SettingsSections } from "@/components/settings/settings-sections";

export function SettingsPage() {
  return (
    <AreaPage areaId="settings">
      <SettingsSections />
      <LocalDisplayNameForm />
    </AreaPage>
  );
}
