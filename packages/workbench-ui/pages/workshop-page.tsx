import { Navigate, Route, Routes, useParams } from "react-router-dom";
import { WorkshopItem } from "../components/workshop/workshop-item";
import { WorkshopList } from "../components/workshop/workshop-list";
import { workshopPaths } from "../lib/marketplace-routes";

// Workshop › item. Reached from the account menu after Applications; the
// Marketplace never edits and the Workshop never installs.
export function WorkshopPage() {
  return <Routes>
    <Route index element={<WorkshopList />} />
    <Route path="connectors/new" element={<WorkshopItem />} />
    <Route path="connectors/:connectorId" element={<ConnectorRoute />} />
    <Route path="*" element={<Navigate to={workshopPaths.list} replace />} />
  </Routes>;
}

function ConnectorRoute() {
  const connectorId = useParams().connectorId ?? "";
  return <WorkshopItem key={connectorId} connectorId={connectorId} />;
}
