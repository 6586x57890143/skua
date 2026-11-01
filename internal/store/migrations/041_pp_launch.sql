-- The pp day a server's launch is armed for, 0 for none: at that day's
-- turn /pp opens to everyone with everything new for the season.
alter table pp_guild add column launch bigint not null default 0;
