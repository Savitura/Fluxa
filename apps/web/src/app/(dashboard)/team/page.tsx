'use client';

export default function TeamPage() {
  return (
    <div className="flex flex-col gap-10 animate-in fade-in slide-in-from-bottom-4 duration-500">
      <header>
        <h1 className="text-[2rem] font-bold tracking-tight">Team Management</h1>
        <p className="text-muted text-[1.05rem] mt-1">Manage organization members and roles.</p>
      </header>

      <div className="glass p-12 flex flex-col items-center gap-4 rounded-2xl text-center max-w-lg mx-auto">
        <p className="text-4xl">👥</p>
        <h3 className="text-xl font-semibold">Coming Soon</h3>
        <p className="text-muted leading-relaxed">
          Team management is not yet available through the API. You will be able to invite members and assign roles here once the feature is released.
        </p>
      </div>
    </div>
  );
}
