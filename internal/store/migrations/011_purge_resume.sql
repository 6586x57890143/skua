-- purge: what a restart has to pick up. now_since is when an unfinished
-- /purge now was asked for, null when none is, and now_channels where it
-- deletes. claimed is a scheduled sweep that started and hasn't finished.
alter table purge_subs
	add column now_since    timestamptz,
	add column now_channels bigint[] not null default '{}',
	add column claimed      boolean not null default false;
