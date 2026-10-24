-- The day a member last picked or changed class: a season's first pick
-- can be taken back free for a short while, and a paid change waits out a
-- cooldown from it.
alter table pp_member add column class_day bigint not null default 0;
