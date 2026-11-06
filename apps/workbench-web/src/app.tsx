import { BrowserRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToastRegion } from "@talgent-work/workbench-ui/components/feedback/toast-region";
import WorkbenchRoutes from "./workbench-routes";

const queryClient = new QueryClient();

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <WorkbenchRoutes />
      </BrowserRouter>
      <ToastRegion />
    </QueryClientProvider>
  );
}
