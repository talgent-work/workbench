import type { ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { AuthGate } from "@talgent/workbench-ui/components/auth/auth-gate";
import { DocumentTitle } from "@talgent/workbench-ui/components/shell/document-title";
import { EntryShellContext, personalPathsAt, type EntryShell } from "@talgent/workbench-ui/components/shell/entry-shell";
import { ComputersPage } from "@talgent/workbench-ui/pages/computers/computers-page";
import { ConnectComputerPage } from "@talgent/workbench-ui/pages/computers/connect-computer-page";
import { LoginPage } from "@talgent/workbench-ui/pages/login-page";
import { MarketplacePage } from "@talgent/workbench-ui/pages/marketplace-page";
import { ProfilePage } from "@talgent/workbench-ui/pages/profile-page";
import { LibraryPage } from "@talgent/workbench-ui/pages/workbench/library-page";
import { NewWorkPage } from "@talgent/workbench-ui/pages/workbench/new-work-page";
import { StandaloneWorkPage } from "@talgent/workbench-ui/pages/workbench/standalone-work-page";
import { StandaloneWorksPage } from "@talgent/workbench-ui/pages/workbench/standalone-works-page";
import { UsagePage } from "@talgent/workbench-ui/pages/workbench/usage-page";
import { WorkbenchLayout } from "./workbench-layout";

const workbenchShell: EntryShell = { Layout: WorkbenchLayout, home: "/", personal: personalPathsAt("") };

/** Talgent Workbench's routes: the person's own pages. */
export default function WorkbenchRoutes() {
  const page = (title: string, children: ReactNode) => <AuthGate><DocumentTitle title={title}>{children}</DocumentTitle></AuthGate>;
  return <EntryShellContext value={workbenchShell}><Routes>
    <Route path="/" element={page("New work", <NewWorkPage />)} />
    <Route path="/login" element={<DocumentTitle title="Sign in"><LoginPage /></DocumentTitle>} />
    <Route path="/works" element={page("Works", <StandaloneWorksPage />)} />
    <Route path="/works/:workId" element={page("Work", <StandaloneWorkPage />)} />
    <Route path="/library" element={page("Library", <LibraryPage />)} />
    <Route path="/usage" element={page("Usage", <UsagePage />)} />
    <Route path="/marketplace/*" element={page("Marketplace", <MarketplacePage />)} />
    <Route path="/profile" element={page("Profile", <ProfilePage />)} />
    <Route path="/computers" element={page("Computers", <ComputersPage />)} />
    <Route path="/computers/connect" element={page("Connect a computer", <ConnectComputerPage />)} />
    <Route path="*" element={<Navigate to="/" replace />} />
  </Routes></EntryShellContext>;
}
