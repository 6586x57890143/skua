-- How many names the heralds' roll holds before a livery of one's own is
-- at its dearest.
alter table pp_guild add column livery_slots bigint not null default 40;
