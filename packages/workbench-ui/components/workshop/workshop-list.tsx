import { Button, Card, Description, Dropdown, Label, Table } from "@heroui/react";
import type { Connector } from "@talgent/contracts/marketplace/public/v1/connectors_pb";
import { ChevronDown, Plus } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { useOwnedConnectors } from "../../lib/connector-workshop";
import { useProviderNames } from "../../lib/marketplace-connections";
import { workshopPaths } from "../../lib/marketplace-routes";
import { ConnectorImage } from "../marketplace/connector-image";
import { LoadState } from "../marketplace/marketplace-shared";
import { PageLayout } from "../shell/page-layout";
import { StateChip } from "./workshop-parts";
import "../marketplace/marketplace.css";

const order = ["private", "published", "withdrawn"];

/** Starts something new; making Plugins comes later, so it offers Connectors. */
export function AddMenu({ size = "sm" }: { size?: "sm" | "md" }) {
  const navigate = useNavigate();
  return <Dropdown>
    <Button size={size} variant="primary"><Plus size={15} aria-hidden="true" />Add<ChevronDown size={14} aria-hidden="true" /></Button>
    <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label="Add" onAction={() => navigate(workshopPaths.newConnector)}>
      <Dropdown.Item id="connector" textValue="Connector"><div className="grid"><Label>Connector</Label><Description>An MCP server, such as one inside your company network</Description></div></Dropdown.Item>
    </Dropdown.Menu></Dropdown.Popover>
  </Dropdown>;
}

/** One table of everything the person made, its status a column; a row opens
 * the item. It shows no install status: installing is the Marketplace's. */
export function WorkshopList() {
  const navigate = useNavigate();
  const owned = useOwnedConnectors();
  const providerName = useProviderNames();
  const items = [...(owned.data ?? [])].sort((a, b) => order.indexOf(a.state) - order.indexOf(b.state) || a.name.localeCompare(b.name));
  const details = (connector: Connector) => connector.provider ? `Signs in with ${providerName(connector.provider)}` : connector.transport === "stdio" ? "Local command" : "No sign-in";
  return <PageLayout workspaceTitle="Workshop" topActions={items.length ? <AddMenu /> : undefined}>
    <div className="mx-auto grid w-[min(1040px,100%)] gap-6 px-4 pb-28 pt-6">
      <LoadState noun="your connectors" isPending={owned.isPending} isError={owned.isError} hasData={Boolean(owned.data)} isFetching={owned.isFetching} onRetry={() => void owned.refetch()} />
      {owned.data && (items.length === 0 ? <Card className="mx-auto w-[min(560px,100%)]">
        <Card.Header><Card.Title>You haven’t made anything yet</Card.Title><Card.Description>Add a connector for an MCP server, such as one inside your company network or a browser tool.</Card.Description></Card.Header>
        <Card.Footer><AddMenu size="md" /></Card.Footer>
      </Card> : <Table>
        <Table.ScrollContainer>
          <Table.Content aria-label="What you made" className="min-w-[680px]" onRowAction={key => navigate(workshopPaths.connector(String(key)))}>
            <Table.Header>
              <Table.Column isRowHeader>Name</Table.Column>
              <Table.Column>Type</Table.Column>
              <Table.Column>Status</Table.Column>
              <Table.Column>Details</Table.Column>
            </Table.Header>
            <Table.Body>{items.map(item => <Table.Row key={item.connectorId} id={item.connectorId} className="cursor-pointer">
              <Table.Cell><span className="flex items-center gap-3"><ConnectorImage connector={item} size={32} />
                <span className="grid min-w-0"><strong className="truncate">{item.name}</strong><span className="line-clamp-1 text-[12px] text-muted">{item.description}</span></span></span></Table.Cell>
              <Table.Cell>Connector</Table.Cell>
              <Table.Cell><StateChip state={item.state} /></Table.Cell>
              <Table.Cell>{details(item)}</Table.Cell>
            </Table.Row>)}</Table.Body>
          </Table.Content>
        </Table.ScrollContainer>
      </Table>)}
    </div>
  </PageLayout>;
}
