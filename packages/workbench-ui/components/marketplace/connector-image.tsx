import { useRef } from "react";
import { Button, Dropdown, Label } from "@heroui/react";
import type { Connector } from "@talgent/contracts/marketplace/public/v1/connectors_pb";
import { ImageUp, Pencil, Trash2 } from "lucide-react";
import { iconTypes, readIcon, useConnectorIcon, type IconImage } from "../../lib/connector-workshop";
import type { MarketplaceOwner } from "../../lib/marketplace-catalog";
import { notify } from "../../lib/notifications";
import { ConnectorIcon } from "./marketplace-icons";

type Shown = Pick<Connector, "connectorId" | "name" | "iconDigest">;

/** An image tile, rounded like the lettered tiles. */
function ImageTile({ url, size }: { url: string; size: number }) {
  return <span className="mk-icon-stack" style={{ width: size, height: size }} aria-hidden="true">
    <img src={url} alt="" className="size-full object-cover ring-1 ring-separator" style={{ borderRadius: Math.round(size * 0.28) }} />
  </span>;
}

/** A Connector's uploaded icon when it has one, otherwise its lettered or
 * brand tile. */
export function ConnectorImage({ connector, size = 40, owner }: { connector: Shown; size?: number; owner?: MarketplaceOwner }) {
  const icon = useConnectorIcon(connector, owner);
  return icon.data ? <ImageTile url={icon.data} size={size} /> : <ConnectorIcon name={connector.name || "?"} size={size} />;
}

/** The Workshop header's icon, which is its own edit control: pressing it
 * offers to upload, replace or remove an image. */
export function EditableConnectorIcon({ name, url, onChange, isPending, size = 72 }: { name: string; url?: string; onChange: (icon?: IconImage) => void; isPending?: boolean; size?: number }) {
  const input = useRef<HTMLInputElement>(null);
  const radius = Math.round(size * 0.28);
  const pick = async (file?: File) => {
    if (!file) return;
    const icon = await readIcon(file);
    if (typeof icon === "string") notify("connector-icon", "danger", "That file can’t be an icon", icon);
    else onChange(icon);
  };
  return <>
    <Dropdown>
      <Button variant="ghost" aria-label="Change icon" isDisabled={isPending} className="group relative h-auto min-w-0 shrink-0 p-0 hover:bg-transparent data-[hovered=true]:bg-transparent" style={{ borderRadius: radius }}>
        {url ? <ImageTile url={url} size={size} /> : <ConnectorIcon name={name || "?"} size={size} />}
        <span className="absolute inset-0 grid place-items-center bg-black/50 text-white opacity-0 transition-opacity group-hover:opacity-100 group-data-[focus-visible=true]:opacity-100 group-data-[pressed=true]:opacity-100 motion-reduce:transition-none" style={{ borderRadius: radius }} aria-hidden="true">
          <span className="grid justify-items-center gap-0.5 text-[11px] font-medium"><Pencil size={16} />Edit</span>
        </span>
      </Button>
      <Dropdown.Popover placement="bottom start"><Dropdown.Menu aria-label="Icon" onAction={key => key === "upload" ? input.current?.click() : onChange(undefined)}>
        <Dropdown.Item id="upload" textValue="Upload image"><ImageUp size={16} /><Label>{url ? "Replace image…" : "Upload image…"}</Label></Dropdown.Item>
        {url ? <Dropdown.Item id="remove" textValue="Remove image" variant="danger"><Trash2 size={16} /><Label>Remove image</Label></Dropdown.Item> : null}
      </Dropdown.Menu></Dropdown.Popover>
    </Dropdown>
    <input ref={input} type="file" accept={iconTypes.join(",")} hidden onChange={event => { void pick(event.target.files?.[0]); event.target.value = ""; }} />
  </>;
}
