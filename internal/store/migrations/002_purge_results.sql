-- purge: which channels the last sweep couldn't read (shown by /purge
-- status), and whether it finished. The count it replaces said neither.
alter table purge_subs
	drop column last_unreachable,
	add column unreachable bigint[] not null default '{}',
	add column last_ok boolean;
